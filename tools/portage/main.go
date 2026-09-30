package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	goruntime "runtime"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	v1alpha1 "github.com/aegis-migrate/portage/api/v1alpha1"
	"github.com/aegis-migrate/portage/controllers"
	"github.com/aegis-migrate/portage/pkg/ast"
	"github.com/aegis-migrate/portage/pkg/cdc"
	"github.com/aegis-migrate/portage/pkg/cutover"
	"github.com/aegis-migrate/portage/pkg/dashboard"
	"github.com/aegis-migrate/portage/pkg/dryrun"
	"github.com/aegis-migrate/portage/pkg/ghostwire"
	"github.com/aegis-migrate/portage/pkg/quiesce"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
	version  = "v1.0.0"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
}

func printUsage() {
	fmt.Fprintf(os.Stdout, `Portage - Zero-Downtime State-Aware Kubernetes Migration Platform

Usage:
  portage <command> [flags]

Available Commands:
  dryrun      Validate a PortageMigration manifest offline prior to execution
  dashboard   Start the local web-based migration observability dashboard
  ghostwire   Run the Ghostwire L4 TCP cutover proxy
  operator    Start the Portage Kubernetes controller manager (default in cluster)
  version     Display Portage version and build information
  help        Show this help message

Flags:
  -h, --help  Show help for portage or any subcommand

Use "portage <command> --help" for more information about a command.
`)
}

func runDryRun(args []string) {
	fs := flag.NewFlagSet("dryrun", flag.ExitOnError)
	var filePath string
	var outputFmt string
	fs.StringVar(&filePath, "f", "", "Path to PortageMigration YAML manifest")
	fs.StringVar(&filePath, "file", "", "Path to PortageMigration YAML manifest")
	fs.StringVar(&outputFmt, "o", "text", "Output format (text, json)")
	fs.StringVar(&outputFmt, "output", "text", "Output format (text, json)")

	fs.Usage = func() {
		fmt.Fprintf(os.Stdout, `Usage: portage dryrun [flags] [manifest.yaml]

Validate a PortageMigration manifest offline prior to execution.

Flags:
  -f, --file string     Path to PortageMigration YAML manifest (or pass as positional arg)
  -o, --output string   Output format: text, json (default: "text")
  -h, --help            Show help for dryrun
`)
	}

	var cleanArgs []string
	var positional []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			cleanArgs = append(cleanArgs, args[i])
			if !strings.Contains(args[i], "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				cleanArgs = append(cleanArgs, args[i+1])
				i++
			}
		} else {
			positional = append(positional, args[i])
		}
	}
	cleanArgs = append(cleanArgs, positional...)
	_ = fs.Parse(cleanArgs)

	// If -f was not passed, check positional argument
	if filePath == "" && fs.NArg() > 0 {
		filePath = fs.Arg(0)
	}

	var data []byte
	var err error

	if filePath == "" || filePath == "-" {
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			// Data being piped into stdin
			data, err = io.ReadAll(os.Stdin)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error reading from stdin: %v\n", err)
				os.Exit(1)
			}
		} else {
			fmt.Fprintln(os.Stderr, "Error: manifest file required. Provide -f <path> or pipe via stdin.")
			fs.Usage()
			os.Exit(1)
		}
	} else {
		data, err = os.ReadFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading manifest %q: %v\n", filePath, err)
			os.Exit(1)
		}
	}

	var migration v1alpha1.PortageMigration
	if err := k8syaml.Unmarshal(data, &migration); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing PortageMigration YAML: %v\n", err)
		os.Exit(1)
	}

	result := dryrun.ValidateMigration(&migration)

	if strings.ToLower(outputFmt) == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(result)
	} else {
		fmt.Print(dryrun.FormatResult(result))
	}

	if !result.Valid {
		os.Exit(1)
	}
}

func runDashboard(args []string) {
	fs := flag.NewFlagSet("dashboard", flag.ExitOnError)
	var port int
	var addr string
	fs.IntVar(&port, "p", 8080, "Dashboard HTTP port")
	fs.IntVar(&port, "port", 8080, "Dashboard HTTP port")
	fs.StringVar(&addr, "addr", "", "Dashboard HTTP bind address (e.g. 0.0.0.0:8080)")

	fs.Usage = func() {
		fmt.Fprintf(os.Stdout, `Usage: portage dashboard [flags]

Start the Portage web-based migration observability dashboard.

Flags:
  -p, --port int       Dashboard HTTP port (default: 8080)
      --addr string    Dashboard HTTP bind address (overrides port)
  -h, --help           Show help for dashboard
`)
	}

	_ = fs.Parse(args)

	listenAddr := addr
	if listenAddr == "" {
		listenAddr = fmt.Sprintf(":%d", port)
	}

	fmt.Printf("[dashboard] Portage Migration Dashboard listening on http://%s\n", listenAddr)
	fmt.Println("[dashboard] Press Ctrl+C to terminate")

	if err := dashboard.Serve(listenAddr, nil); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting dashboard: %v\n", err)
		os.Exit(1)
	}
}

func runOperator(args []string) {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string

	fs := flag.NewFlagSet("operator", flag.ExitOnError)
	fs.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	fs.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	fs.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager.")

	opts := zap.Options{Development: true}
	opts.BindFlags(fs)
	_ = fs.Parse(args)

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "portage-migration-lock.portage.sh",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	kubeClient, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		setupLog.Error(err, "unable to create kubernetes clientset")
		os.Exit(1)
	}

	hookExec := quiesce.NewK8sHookExecutor(kubeClient, nil)
	astEngine := ast.NewASTTranslationEngine()
	cdcClient := cdc.NewHTTPCDCClient(15 * time.Second)
	cutoverClient := cutover.NewHTTPCutoverClient(30 * time.Second)

	if err = (&controllers.PortageMigrationReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		HookExecutor:  hookExec,
		ASTEngine:     astEngine,
		CDCClient:     cdcClient,
		CutoverClient: cutoverClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "PortageMigration")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting Portage.sh controller manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func main() {
	if len(os.Args) < 2 {
		// If running in cluster, default to operator
		if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
			runOperator(nil)
			return
		}
		printUsage()
		return
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "dryrun":
		runDryRun(args)
	case "dashboard":
		runDashboard(args)
	case "ghostwire":
		if err := ghostwire.RunArgs(args); err != nil {
			fmt.Fprintf(os.Stderr, "ghostwire error: %v\n", err)
			os.Exit(1)
		}
	case "operator":
		runOperator(args)
	case "version", "--version", "-v":
		fmt.Printf("Portage Enterprise Migration Suite %s (built with: %s)\n", version, goruntime.Version())
		fmt.Println("Architecture: Open-Core Pluggable Driver SPI")
		fmt.Println("License: BSL 1.1 (Non-Compete)")
	case "help", "--help", "-h":
		printUsage()
	default:
		// Backward compatibility: If arguments start with "-" (e.g. --metrics-bind-address), treat as operator
		if strings.HasPrefix(cmd, "-") {
			runOperator(os.Args[1:])
			return
		}
		fmt.Fprintf(os.Stderr, "Unknown command: %q\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

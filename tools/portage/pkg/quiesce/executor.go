package quiesce

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	v1alpha1 "github.com/aegis-migrate/portage/api/v1alpha1"
)

// HookExecutor runs quiescence hooks against database or storage workloads with backoff retries.
type HookExecutor interface {
	ExecutePreQuiesce(ctx context.Context, migration *v1alpha1.PortageMigration) error
	ExecutePostQuiesce(ctx context.Context, migration *v1alpha1.PortageMigration) error
}

// PodCommandRunner abstracts remote command execution inside containers.
type PodCommandRunner interface {
	RunCommand(ctx context.Context, namespace, pod, container string, cmd []string) (string, error)
}

type K8sHookExecutor struct {
	client     kubernetes.Interface
	runner     PodCommandRunner
	httpClient *http.Client
}

func NewK8sHookExecutor(client kubernetes.Interface, runner PodCommandRunner) *K8sHookExecutor {
	return &K8sHookExecutor{
		client:     client,
		runner:     runner,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (e *K8sHookExecutor) ExecutePreQuiesce(ctx context.Context, migration *v1alpha1.PortageMigration) error {
	for _, hook := range migration.Spec.PreQuiesceHooks {
		if err := e.executeHookWithRetry(ctx, migration.Spec.SourceNamespace, hook); err != nil {
			return fmt.Errorf("pre-quiesce hook %q failed: %w", hook.Name, err)
		}
	}
	return nil
}

func (e *K8sHookExecutor) ExecutePostQuiesce(ctx context.Context, migration *v1alpha1.PortageMigration) error {
	for _, hook := range migration.Spec.PostQuiesceHooks {
		if err := e.executeHookWithRetry(ctx, migration.Spec.SourceNamespace, hook); err != nil {
			return fmt.Errorf("post-quiesce hook %q failed: %w", hook.Name, err)
		}
	}
	return nil
}

func (e *K8sHookExecutor) executeHookWithRetry(ctx context.Context, namespace string, hook v1alpha1.QuiescenceHook) error {
	maxRetries := int(hook.MaxRetries)
	if maxRetries <= 0 {
		maxRetries = 3
	}
	timeoutSec := int(hook.TimeoutSeconds)
	if timeoutSec <= 0 {
		timeoutSec = 30
	}

	hookCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		select {
		case <-hookCtx.Done():
			return fmt.Errorf("hook %s aborted: %w", hook.Name, hookCtx.Err())
		default:
		}

		err := e.dispatchHook(hookCtx, namespace, hook)
		if err == nil {
			return nil
		}
		lastErr = err

		// Exponential backoff with jitter: base 100ms, cap 5s
		backoff := time.Duration(float64(100*time.Millisecond) * math.Pow(2, float64(attempt)))
		jitter := time.Duration(rand.Int63n(int64(backoff / 4)))
		totalWait := backoff + jitter
		if totalWait > 5*time.Second {
			totalWait = 5 * time.Second
		}

		select {
		case <-time.After(totalWait):
		case <-hookCtx.Done():
			return fmt.Errorf("hook %s timed out during backoff: %w", hook.Name, hookCtx.Err())
		}
	}

	return fmt.Errorf("exhausted %d retries, last error: %w", maxRetries, lastErr)
}

func (e *K8sHookExecutor) dispatchHook(ctx context.Context, namespace string, hook v1alpha1.QuiescenceHook) error {
	switch hook.Type {
	case v1alpha1.HookTypePodExec:
		if hook.PodExec == nil {
			return fmt.Errorf("podExec configuration missing for hook %s", hook.Name)
		}
		return e.execInPod(ctx, namespace, hook.PodExec)
	case v1alpha1.HookTypeHTTP:
		if hook.HTTP == nil {
			return fmt.Errorf("http configuration missing for hook %s", hook.Name)
		}
		return e.execHTTP(ctx, hook.HTTP)
	default:
		return fmt.Errorf("unsupported hook type: %s", hook.Type)
	}
}

func (e *K8sHookExecutor) execInPod(ctx context.Context, namespace string, execSpec *v1alpha1.PodExecHook) error {
	selector, err := metav1.LabelSelectorAsSelector(&execSpec.Selector)
	if err != nil {
		return fmt.Errorf("invalid label selector: %w", err)
	}

	podList, err := e.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return fmt.Errorf("failed listing pods for exec: %w", err)
	}
	if len(podList.Items) == 0 {
		return fmt.Errorf("no pods matching selector %s in namespace %s", selector.String(), namespace)
	}

	targetPod := podList.Items[0]
	container := execSpec.Container
	if container == "" {
		container = targetPod.Spec.Containers[0].Name
	}

	if e.runner != nil {
		_, err := e.runner.RunCommand(ctx, namespace, targetPod.Name, container, execSpec.Command)
		return err
	}

	return nil
}

func (e *K8sHookExecutor) execHTTP(ctx context.Context, httpSpec *v1alpha1.HTTPHook) error {
	req, err := http.NewRequestWithContext(ctx, httpSpec.Method, httpSpec.URL, nil)
	if err != nil {
		return fmt.Errorf("invalid HTTP request: %w", err)
	}
	for k, v := range httpSpec.Headers {
		req.Header.Set(k, v)
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP hook returned non-2xx status code: %d", resp.StatusCode)
	}
	return nil
}

package ast

import (
	"context"
	"fmt"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// ManifestTranslator defines the interface for intercepting and transforming
// deprecated or environment-specific Kubernetes manifests into compliant target manifests.
type ManifestTranslator interface {
	// CanTranslate returns true if this translator can handle the given GroupVersionKind
	CanTranslate(gvk schema.GroupVersionKind) bool
	// Translate transforms raw YAML manifest from source format to modern target format
	Translate(ctx context.Context, sourceYAML []byte, targetNamespace string) ([]byte, error)
	// TargetGVK returns the modernized GroupVersionKind produced by this translator
	TargetGVK() schema.GroupVersionKind
}

// IngressASTTranslator handles migration from legacy extensions/v1beta1
// or networking.k8s.io/v1beta1 to modern networking.k8s.io/v1 Ingress specs.
type IngressASTTranslator struct {
	DefaultPathType        string
	IngressClassAnnotation string
}

func NewIngressASTTranslator() *IngressASTTranslator {
	return &IngressASTTranslator{
		DefaultPathType:        "Prefix",
		IngressClassAnnotation: "kubernetes.io/ingress.class",
	}
}

func (t *IngressASTTranslator) CanTranslate(gvk schema.GroupVersionKind) bool {
	if gvk.Kind != "Ingress" {
		return false
	}
	return (gvk.Group == "extensions" && gvk.Version == "v1beta1") ||
		(gvk.Group == "networking.k8s.io" && gvk.Version == "v1beta1")
}

func (t *IngressASTTranslator) TargetGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   "networking.k8s.io",
		Version: "v1",
		Kind:    "Ingress",
	}
}

func (t *IngressASTTranslator) Translate(ctx context.Context, sourceYAML []byte, targetNamespace string) ([]byte, error) {
	// Parse raw YAML into generic yaml.Node to retain AST structure, comments, and node hierarchy
	var root yaml.Node
	if err := yaml.Unmarshal(sourceYAML, &root); err != nil {
		return nil, fmt.Errorf("failed to parse manifest into YAML AST: %w", err)
	}

	if len(root.Content) == 0 {
		return nil, fmt.Errorf("empty YAML document")
	}

	docNode := root.Content[0]
	if docNode.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected mapping node at root, got kind %d", docNode.Kind)
	}

	// 1. Update apiVersion to networking.k8s.io/v1
	if err := setMapFieldValue(docNode, "apiVersion", "networking.k8s.io/v1"); err != nil {
		return nil, fmt.Errorf("failed to update apiVersion: %w", err)
	}

	// 2. Handle metadata: namespace remapping and ingress-class annotation extraction
	metadataNode := findMapValue(docNode, "metadata")
	var ingressClassVal string
	if metadataNode != nil {
		if targetNamespace != "" {
			_ = setMapFieldValue(metadataNode, "namespace", targetNamespace)
		}
		annotationsNode := findMapValue(metadataNode, "annotations")
		if annotationsNode != nil {
			if classValNode := findMapValue(annotationsNode, t.IngressClassAnnotation); classValNode != nil {
				ingressClassVal = classValNode.Value
				// Remove legacy annotation
				deleteMapField(annotationsNode, t.IngressClassAnnotation)
			}
		}
	}

	// 3. Mutate spec
	specNode := findMapValue(docNode, "spec")
	if specNode != nil {
		// If ingress class was found in annotations and spec.ingressClassName is missing, set it
		if ingressClassVal != "" && findMapValue(specNode, "ingressClassName") == nil {
			_ = setMapFieldValue(specNode, "ingressClassName", ingressClassVal)
		}

		// Mutate default backend if present
		if defaultBackendNode := findMapValue(specNode, "backend"); defaultBackendNode != nil {
			if err := t.transformBackendNode(defaultBackendNode); err != nil {
				return nil, fmt.Errorf("failed to transform default backend: %w", err)
			}
		}

		// Mutate rules -> http -> paths
		rulesNode := findMapValue(specNode, "rules")
		if rulesNode != nil && rulesNode.Kind == yaml.SequenceNode {
			for _, ruleItem := range rulesNode.Content {
				httpNode := findMapValue(ruleItem, "http")
				if httpNode == nil {
					continue
				}
				pathsNode := findMapValue(httpNode, "paths")
				if pathsNode == nil || pathsNode.Kind != yaml.SequenceNode {
					continue
				}
				for _, pathItem := range pathsNode.Content {
					// Ensure pathType is present (mandatory in networking.k8s.io/v1)
					if findMapValue(pathItem, "pathType") == nil {
						_ = setMapFieldValue(pathItem, "pathType", t.DefaultPathType)
					}
					// Transform path backend
					backendNode := findMapValue(pathItem, "backend")
					if backendNode != nil {
						if err := t.transformBackendNode(backendNode); err != nil {
							return nil, fmt.Errorf("failed to transform path backend: %w", err)
						}
					}
				}
			}
		}
	}

	out, err := yaml.Marshal(&root)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize AST to YAML: %w", err)
	}

	return out, nil
}

// transformBackendNode morphs legacy backend:
//   backend:
//     serviceName: foo
//     servicePort: 8080 | "http"
// into modern:
//   backend:
//     service:
//       name: foo
//       port:
//         number: 8080  (or name: "http")
func (t *IngressASTTranslator) transformBackendNode(backendNode *yaml.Node) error {
	svcNameNode := findMapValue(backendNode, "serviceName")
	svcPortNode := findMapValue(backendNode, "servicePort")

	// If already in new format, skip
	if findMapValue(backendNode, "service") != nil {
		return nil
	}

	if svcNameNode == nil || svcPortNode == nil {
		// Could be a resource backend; leave as is
		return nil
	}

	svcName := svcNameNode.Value
	var portKey string
	var portVal string

	// Determine if port is integer or named string
	if svcPortNode.Tag == "!!int" || isDigits(svcPortNode.Value) {
		portKey = "number"
		portVal = svcPortNode.Value
	} else {
		portKey = "name"
		portVal = svcPortNode.Value
	}

	// Delete legacy fields
	deleteMapField(backendNode, "serviceName")
	deleteMapField(backendNode, "servicePort")

	// Construct nested service node
	serviceNode := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "name"},
			{Kind: yaml.ScalarNode, Value: svcName},
			{Kind: yaml.ScalarNode, Value: "port"},
			{
				Kind: yaml.MappingNode,
				Content: []*yaml.Node{
					{Kind: yaml.ScalarNode, Value: portKey},
					{
						Kind:  yaml.ScalarNode,
						Tag:   func() string { if portKey == "number" { return "!!int" } ; return "!!str" }(),
						Value: portVal,
					},
				},
			},
		},
	}

	backendNode.Content = append(backendNode.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "service"},
		serviceNode,
	)

	return nil
}

// Registry coordinates multiple AST translators for manifests.
type ASTTranslationEngine struct {
	translators []ManifestTranslator
}

func NewASTTranslationEngine() *ASTTranslationEngine {
	return &ASTTranslationEngine{
		translators: []ManifestTranslator{
			NewIngressASTTranslator(),
		},
	}
}

func (e *ASTTranslationEngine) RegisterTranslator(t ManifestTranslator) {
	e.translators = append(e.translators, t)
}

func (e *ASTTranslationEngine) TranslateManifest(ctx context.Context, rawYAML []byte, targetNamespace string) ([]byte, schema.GroupVersionKind, error) {
	// Probe GVK using unstructured decoder
	var u unstructured.Unstructured
	if err := yaml.Unmarshal(rawYAML, &u.Object); err != nil {
		return nil, schema.GroupVersionKind{}, fmt.Errorf("invalid Kubernetes manifest: %w", err)
	}

	gvk := u.GroupVersionKind()
	for _, tr := range e.translators {
		if tr.CanTranslate(gvk) {
			translated, err := tr.Translate(ctx, rawYAML, targetNamespace)
			if err != nil {
				return nil, schema.GroupVersionKind{}, fmt.Errorf("translation failed for %s: %w", gvk.String(), err)
			}
			return translated, tr.TargetGVK(), nil
		}
	}

	// If no translator matched, return unchanged with target namespace modified if applicable
	if targetNamespace != "" {
		u.SetNamespace(targetNamespace)
		out, err := yaml.Marshal(u.Object)
		return out, gvk, err
	}

	return rawYAML, gvk, nil
}

// AST Helper functions

func findMapValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func setMapFieldValue(node *yaml.Node, key string, val string) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("cannot set field on non-mapping node")
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			node.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Value: val}
			return nil
		}
	}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Value: val},
	)
	return nil
}

func deleteMapField(node *yaml.Node, key string) {
	if node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			node.Content = append(node.Content[:i], node.Content[i+2:]...)
			return
		}
	}
}

func isDigits(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

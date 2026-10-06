package ast

import (
	"context"
	"fmt"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// TranslationOptions configures target transformations
type TranslationOptions struct {
	TargetNamespace        string
	TargetStorageClassName string
}

// ManifestTranslator defines the interface for transforming Kubernetes manifests
type ManifestTranslator interface {
	CanTranslate(gvk schema.GroupVersionKind) bool
	Translate(ctx context.Context, sourceYAML []byte, opts TranslationOptions) ([]byte, error)
	TargetGVK() schema.GroupVersionKind
}

// IngressASTTranslator migrates legacy Ingresses to networking.k8s.io/v1
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

func (t *IngressASTTranslator) Translate(ctx context.Context, sourceYAML []byte, opts TranslationOptions) ([]byte, error) {
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

	// 1. Mutate apiVersion to networking.k8s.io/v1
	if err := setMapFieldValue(docNode, "apiVersion", "networking.k8s.io/v1"); err != nil {
		return nil, fmt.Errorf("failed to update apiVersion: %w", err)
	}

	// 2. Clean status
	deleteMapField(docNode, "status")

	// 3. Namespace remapping & ingress-class annotation extraction
	metadataNode := findMapValue(docNode, "metadata")
	var ingressClassVal string
	if metadataNode != nil {
		stripClusterMetadata(metadataNode)
		if opts.TargetNamespace != "" {
			_ = setMapFieldValue(metadataNode, "namespace", opts.TargetNamespace)
		}
		annotationsNode := findMapValue(metadataNode, "annotations")
		if annotationsNode != nil {
			if classValNode := findMapValue(annotationsNode, t.IngressClassAnnotation); classValNode != nil {
				ingressClassVal = classValNode.Value
				deleteMapField(annotationsNode, t.IngressClassAnnotation)
			}
		}
	}

	// 4. Mutate spec fields
	specNode := findMapValue(docNode, "spec")
	if specNode != nil {
		if ingressClassVal != "" && findMapValue(specNode, "ingressClassName") == nil {
			_ = setMapFieldValue(specNode, "ingressClassName", ingressClassVal)
		}

		if defaultBackendNode := findMapValue(specNode, "backend"); defaultBackendNode != nil {
			if err := t.transformBackendNode(defaultBackendNode); err != nil {
				return nil, fmt.Errorf("failed to transform default backend: %w", err)
			}
		}

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
					if findMapValue(pathItem, "pathType") == nil {
						_ = setMapFieldValue(pathItem, "pathType", t.DefaultPathType)
					}
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

	return yaml.Marshal(&root)
}

func (t *IngressASTTranslator) transformBackendNode(backendNode *yaml.Node) error {
	svcNameNode := findMapValue(backendNode, "serviceName")
	svcPortNode := findMapValue(backendNode, "servicePort")

	if findMapValue(backendNode, "service") != nil || svcNameNode == nil || svcPortNode == nil {
		return nil
	}

	svcName := svcNameNode.Value
	var portKey string
	var portVal string

	if svcPortNode.Tag == "!!int" || isDigits(svcPortNode.Value) {
		portKey = "number"
		portVal = svcPortNode.Value
	} else {
		portKey = "name"
		portVal = svcPortNode.Value
	}

	deleteMapField(backendNode, "serviceName")
	deleteMapField(backendNode, "servicePort")

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
						Tag:   func() string { if portKey == "number" { return "!!int" }; return "!!str" }(),
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

// StatefulSetASTTranslator translates apps/v1beta1, apps/v1beta2 to apps/v1
// and remaps StorageClasses in volumeClaimTemplates
type StatefulSetASTTranslator struct{}

func NewStatefulSetASTTranslator() *StatefulSetASTTranslator {
	return &StatefulSetASTTranslator{}
}

func (t *StatefulSetASTTranslator) CanTranslate(gvk schema.GroupVersionKind) bool {
	if gvk.Kind != "StatefulSet" {
		return false
	}
	return gvk.Group == "apps" || gvk.Group == ""
}

func (t *StatefulSetASTTranslator) TargetGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   "apps",
		Version: "v1",
		Kind:    "StatefulSet",
	}
}

func (t *StatefulSetASTTranslator) Translate(ctx context.Context, sourceYAML []byte, opts TranslationOptions) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(sourceYAML, &root); err != nil {
		return nil, fmt.Errorf("failed to parse StatefulSet YAML AST: %w", err)
	}

	if len(root.Content) == 0 {
		return nil, fmt.Errorf("empty YAML document")
	}

	docNode := root.Content[0]
	if docNode.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected mapping node at root")
	}

	// 1. Upgrade apiVersion to apps/v1
	_ = setMapFieldValue(docNode, "apiVersion", "apps/v1")

	// 2. Clean status
	deleteMapField(docNode, "status")

	// 3. Remap metadata namespace & strip cluster runtime fields
	metadataNode := findMapValue(docNode, "metadata")
	if metadataNode != nil {
		stripClusterMetadata(metadataNode)
		if opts.TargetNamespace != "" {
			_ = setMapFieldValue(metadataNode, "namespace", opts.TargetNamespace)
		}
	}

	// 4. Spec transformations: volumeClaimTemplates StorageClass remapping
	specNode := findMapValue(docNode, "spec")
	if specNode != nil {
		vctNode := findMapValue(specNode, "volumeClaimTemplates")
		if vctNode != nil && vctNode.Kind == yaml.SequenceNode && opts.TargetStorageClassName != "" {
			for _, vctItem := range vctNode.Content {
				vctSpec := findMapValue(vctItem, "spec")
				if vctSpec != nil {
					_ = setMapFieldValue(vctSpec, "storageClassName", opts.TargetStorageClassName)
				}
			}
		}

		// Ensure selector exists (mandatory in apps/v1)
		selectorNode := findMapValue(specNode, "selector")
		if selectorNode == nil {
			templateNode := findMapValue(specNode, "template")
			if templateNode != nil {
				templateMeta := findMapValue(templateNode, "metadata")
				if templateMeta != nil {
					labelsNode := findMapValue(templateMeta, "labels")
					if labelsNode != nil {
						newSelector := &yaml.Node{
							Kind: yaml.MappingNode,
							Content: []*yaml.Node{
								{Kind: yaml.ScalarNode, Value: "matchLabels"},
								labelsNode,
							},
						}
						specNode.Content = append(specNode.Content,
							&yaml.Node{Kind: yaml.ScalarNode, Value: "selector"},
							newSelector,
						)
					}
				}
			}
		}
	}

	return yaml.Marshal(&root)
}

// ServiceASTTranslator translates Services (cleaning clusterIP and remapping namespace)
type ServiceASTTranslator struct{}

func NewServiceASTTranslator() *ServiceASTTranslator {
	return &ServiceASTTranslator{}
}

func (t *ServiceASTTranslator) CanTranslate(gvk schema.GroupVersionKind) bool {
	return gvk.Kind == "Service" && (gvk.Group == "" || gvk.Group == "core")
}

func (t *ServiceASTTranslator) TargetGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   "",
		Version: "v1",
		Kind:    "Service",
	}
}

func (t *ServiceASTTranslator) Translate(ctx context.Context, sourceYAML []byte, opts TranslationOptions) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(sourceYAML, &root); err != nil {
		return nil, fmt.Errorf("failed to parse Service YAML AST: %w", err)
	}

	if len(root.Content) == 0 {
		return nil, fmt.Errorf("empty YAML document")
	}

	docNode := root.Content[0]
	if docNode.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected mapping node at root")
	}

	_ = setMapFieldValue(docNode, "apiVersion", "v1")
	deleteMapField(docNode, "status")

	metadataNode := findMapValue(docNode, "metadata")
	if metadataNode != nil {
		stripClusterMetadata(metadataNode)
		if opts.TargetNamespace != "" {
			_ = setMapFieldValue(metadataNode, "namespace", opts.TargetNamespace)
		}
	}

	specNode := findMapValue(docNode, "spec")
	if specNode != nil {
		// Strip static allocated IPs so target cluster allocates cleanly
		deleteMapField(specNode, "clusterIP")
		deleteMapField(specNode, "clusterIPs")
	}

	return yaml.Marshal(&root)
}

// Registry coordinates multiple AST translators for manifests.
type ASTTranslationEngine struct {
	translators []ManifestTranslator
}

func NewASTTranslationEngine() *ASTTranslationEngine {
	return &ASTTranslationEngine{
		translators: []ManifestTranslator{
			NewIngressASTTranslator(),
			NewStatefulSetASTTranslator(),
			NewServiceASTTranslator(),
		},
	}
}

func (e *ASTTranslationEngine) RegisterTranslator(t ManifestTranslator) {
	e.translators = append(e.translators, t)
}

func (e *ASTTranslationEngine) TranslateManifest(ctx context.Context, rawYAML []byte, opts TranslationOptions) ([]byte, schema.GroupVersionKind, error) {
	var u unstructured.Unstructured
	if err := yaml.Unmarshal(rawYAML, &u.Object); err != nil {
		return nil, schema.GroupVersionKind{}, fmt.Errorf("invalid Kubernetes manifest: %w", err)
	}

	gvk := u.GroupVersionKind()
	for _, tr := range e.translators {
		if tr.CanTranslate(gvk) {
			translated, err := tr.Translate(ctx, rawYAML, opts)
			if err != nil {
				return nil, schema.GroupVersionKind{}, fmt.Errorf("translation failed for %s: %w", gvk.String(), err)
			}
			return translated, tr.TargetGVK(), nil
		}
	}

	// Default fallback: mutate namespace and strip cluster metadata
	if opts.TargetNamespace != "" {
		u.SetNamespace(opts.TargetNamespace)
	}
	delete(u.Object, "status")
	metadata, ok := u.Object["metadata"].(map[string]interface{})
	if ok {
		delete(metadata, "resourceVersion")
		delete(metadata, "uid")
		delete(metadata, "creationTimestamp")
		delete(metadata, "generation")
	}
	out, err := yaml.Marshal(u.Object)
	return out, gvk, err
}

func (e *ASTTranslationEngine) TranslateResource(ctx context.Context, u *unstructured.Unstructured, opts TranslationOptions) (*unstructured.Unstructured, error) {
	raw, err := yaml.Marshal(u.Object)
	if err != nil {
		return nil, err
	}
	translated, _, err := e.TranslateManifest(ctx, raw, opts)
	if err != nil {
		return nil, err
	}
	var res unstructured.Unstructured
	if err := yaml.Unmarshal(translated, &res.Object); err != nil {
		return nil, err
	}
	return &res, nil
}

// AST Helper functions

func stripClusterMetadata(metadataNode *yaml.Node) {
	deleteMapField(metadataNode, "resourceVersion")
	deleteMapField(metadataNode, "uid")
	deleteMapField(metadataNode, "creationTimestamp")
	deleteMapField(metadataNode, "generation")
	deleteMapField(metadataNode, "selfLink")
}

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

func setMapFieldValue(node *yaml.Node, key, val string) error {
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

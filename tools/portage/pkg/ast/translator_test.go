package ast

import (
	"context"
	"testing"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestIngressASTTranslation(t *testing.T) {
	legacyIngressYAML := `
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  name: stateful-db-ingress
  namespace: legacy-prod
  annotations:
    kubernetes.io/ingress.class: nginx
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
spec:
  backend:
    serviceName: db-primary
    servicePort: 5432
  rules:
  - host: db.internal.example.com
    http:
      paths:
      - path: /metrics
        backend:
          serviceName: db-metrics
          servicePort: 9187
      - path: /api
        backend:
          serviceName: db-api
          servicePort: http-web
`

	engine := NewASTTranslationEngine()
	ctx := context.Background()

	targetNamespace := "modern-prod"
	translatedYAML, gvk, err := engine.TranslateManifest(ctx, []byte(legacyIngressYAML), targetNamespace)
	if err != nil {
		t.Fatalf("unexpected translation failure: %v", err)
	}

	expectedGVK := schema.GroupVersionKind{
		Group:   "networking.k8s.io",
		Version: "v1",
		Kind:    "Ingress",
	}
	if gvk != expectedGVK {
		t.Errorf("expected GVK %v, got %v", expectedGVK, gvk)
	}

	// Parse back to inspect transformed AST structure
	var parsed map[string]interface{}
	if err := yaml.Unmarshal(translatedYAML, &parsed); err != nil {
		t.Fatalf("failed unmarshaling transformed YAML: %v", err)
	}

	// 1. Verify apiVersion
	if parsed["apiVersion"] != "networking.k8s.io/v1" {
		t.Errorf("expected apiVersion networking.k8s.io/v1, got %v", parsed["apiVersion"])
	}

	// 2. Verify metadata namespace
	metadata := parsed["metadata"].(map[string]interface{})
	if metadata["namespace"] != "modern-prod" {
		t.Errorf("expected namespace modern-prod, got %v", metadata["namespace"])
	}

	// 3. Verify ingressClassName migration from annotations
	spec := parsed["spec"].(map[string]interface{})
	if spec["ingressClassName"] != "nginx" {
		t.Errorf("expected spec.ingressClassName nginx, got %v", spec["ingressClassName"])
	}

	// Verify legacy annotation was stripped
	if annotations, ok := metadata["annotations"].(map[string]interface{}); ok {
		if _, exists := annotations["kubernetes.io/ingress.class"]; exists {
			t.Errorf("expected kubernetes.io/ingress.class annotation to be stripped")
		}
	}

	// 4. Verify default backend structure
	defaultBackend := spec["backend"].(map[string]interface{})
	svc := defaultBackend["service"].(map[string]interface{})
	if svc["name"] != "db-primary" {
		t.Errorf("expected backend service name db-primary, got %v", svc["name"])
	}
	port := svc["port"].(map[string]interface{})
	if port["number"] != 5432 {
		t.Errorf("expected backend service port number 5432, got %v", port["number"])
	}

	// 5. Verify rules paths: pathType injected and port types handled
	rules := spec["rules"].([]interface{})
	rule0 := rules[0].(map[string]interface{})
	httpObj := rule0["http"].(map[string]interface{})
	paths := httpObj["paths"].([]interface{})

	// Path 0: numeric port
	p0 := paths[0].(map[string]interface{})
	if p0["pathType"] != "Prefix" {
		t.Errorf("expected pathType Prefix, got %v", p0["pathType"])
	}
	p0Backend := p0["backend"].(map[string]interface{})
	p0Svc := p0Backend["service"].(map[string]interface{})
	p0Port := p0Svc["port"].(map[string]interface{})
	if p0Port["number"] != 9187 {
		t.Errorf("expected numeric port 9187, got %v", p0Port["number"])
	}

	// Path 1: named string port
	p1 := paths[1].(map[string]interface{})
	if p1["pathType"] != "Prefix" {
		t.Errorf("expected pathType Prefix, got %v", p1["pathType"])
	}
	p1Backend := p1["backend"].(map[string]interface{})
	p1Svc := p1Backend["service"].(map[string]interface{})
	p1Port := p1Svc["port"].(map[string]interface{})
	if p1Port["name"] != "http-web" {
		t.Errorf("expected named string port http-web, got %v", p1Port["name"])
	}
}

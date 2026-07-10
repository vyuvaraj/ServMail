package template

import (
	"testing"
)

func TestRenderTemplateSimple(t *testing.T) {
	out, err := RenderTemplate("Hello {{.Name}}", map[string]interface{}{"Name": "Bob"})
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	if out != "Hello Bob" {
		t.Errorf("expected 'Hello Bob', got %q", out)
	}
}

func TestRenderTemplateEmpty(t *testing.T) {
	out, err := RenderTemplate("", nil)
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	if out != "" {
		t.Errorf("expected empty string, got %q", out)
	}
}

func TestRenderTemplateInvalidSyntax(t *testing.T) {
	_, err := RenderTemplate("Hello {{.Name", map[string]interface{}{"Name": "Bob"})
	if err == nil {
		t.Error("expected template parse error")
	}
}

func TestRenderTemplateMissingKey(t *testing.T) {
	out, err := RenderTemplate("Hello {{.Absent}}", map[string]interface{}{})
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	if out != "Hello <no value>" {
		t.Errorf("expected missing key output, got %q", out)
	}
}

func TestRenderTemplateCondition(t *testing.T) {
	tmpl := "{{if .Show}}Yes{{else}}No{{end}}"
	out, err := RenderTemplate(tmpl, map[string]interface{}{"Show": true})
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	if out != "Yes" {
		t.Errorf("expected 'Yes', got %q", out)
	}
}

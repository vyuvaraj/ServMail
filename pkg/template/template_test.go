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
	// With missingkey=error, referencing an absent variable must return an error,
	// not silently produce "<no value>".
	_, err := RenderTemplate("Hello {{.Absent}}", map[string]interface{}{})
	if err == nil {
		t.Error("expected an error for missing template variable, got nil")
	}
}

// TestRenderTemplateMissingVariableGraceful is the D.55 acceptance test:
// multiple missing variables all produce graceful errors, never panics.
func TestRenderTemplateMissingVariableGraceful(t *testing.T) {
	cases := []struct {
		name   string
		tmpl   string
		ctx    map[string]interface{}
	}{
		{"single missing", "Dear {{.Name}},", map[string]interface{}{}},
		{"multiple missing", "{{.Greeting}} {{.Name}}, your order {{.OrderID}} is ready.", map[string]interface{}{}},
		{"partial context", "Hello {{.Name}}, code {{.Code}}", map[string]interface{}{"Name": "Alice"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RenderTemplate(tc.tmpl, tc.ctx)
			if err == nil {
				t.Errorf("expected error for missing variable in %q, got nil", tc.tmpl)
			}
		})
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

type mockTemplateEngine struct{}

func (m *mockTemplateEngine) Render(tmpl string, ctx map[string]interface{}) (string, error) {
	return "mocked-rendered-content", nil
}

func TestPluggableTemplateEngine(t *testing.T) {
	ActiveTemplateEngine = &mockTemplateEngine{}
	defer func() { ActiveTemplateEngine = nil }()

	out, err := RenderTemplate("any-template-text", nil)
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	if out != "mocked-rendered-content" {
		t.Errorf("expected 'mocked-rendered-content', got %q", out)
	}
}

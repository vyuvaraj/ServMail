package template

import (
	"bytes"
	"text/template"
)

// TemplateEngine defines pluggable compilation and rendering capabilities (such as HTML/MJML engines).
type TemplateEngine interface {
	Render(templateText string, context map[string]interface{}) (string, error)
}

// ActiveTemplateEngine is the globally registered template rendering engine hook.
var ActiveTemplateEngine TemplateEngine

// RenderTemplate compiles and executes a Go template string with a context.
func RenderTemplate(templateText string, context map[string]interface{}) (string, error) {
	if ActiveTemplateEngine != nil {
		return ActiveTemplateEngine.Render(templateText, context)
	}

	tmpl, err := template.New("mail_template").Parse(templateText)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, context); err != nil {
		return "", err
	}
	return buf.String(), nil
}

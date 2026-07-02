package template

import (
	"bytes"
	"text/template"
)

// RenderTemplate compiles and executes a Go template string with a context.
func RenderTemplate(templateText string, context map[string]interface{}) (string, error) {
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

// Package compose handles source files; Docker Compose remains the authority for effective configuration.
package compose

import (
	"fmt"
	"go.yaml.in/yaml/v3"
	"nextcloud-updater/internal/domain"
	"regexp"
	"strings"
)

func child(node *yaml.Node, key string) (*yaml.Node, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected YAML mapping")
	}
	var found *yaml.Node
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			if found != nil {
				return nil, fmt.Errorf("duplicate YAML key %s", key)
			}
			found = node.Content[i+1]
		}
	}
	if found == nil {
		return nil, fmt.Errorf("missing YAML key %s", key)
	}
	return found, nil
}
func ImageNode(source []byte) (*yaml.Node, error) {
	return serviceImageNode(source, "app")
}
func serviceImageNode(source []byte, service string) (*yaml.Node, error) {
	var doc yaml.Node
	if e := yaml.Unmarshal(source, &doc); e != nil {
		return nil, fmt.Errorf("invalid Compose YAML")
	}
	if len(doc.Content) != 1 {
		return nil, fmt.Errorf("expected one Compose document")
	}
	n, e := child(doc.Content[0], "services")
	if e != nil {
		return nil, e
	}
	n, e = child(n, service)
	if e != nil {
		return nil, e
	}
	n, e = child(n, "image")
	if e != nil {
		return nil, e
	}
	if n.Kind != yaml.ScalarNode {
		return nil, fmt.Errorf("an explicit scalar Nextcloud image is required")
	}
	return n, nil
}

var scalarLine = regexp.MustCompile(`^(\s*(?:image|"image"|'image')\s*:\s*)(?:"[^"\r\n]*"|'(?:[^']|'')*'|[^\s#]+)(\s*(?:#.*)?)$`)
var environmentLine = regexp.MustCompile(`^([A-Z][A-Z0-9_]*=)[^\r\n]*(\r?\n?)$`)

func ReplaceEnvironment(source []byte, key, value string) ([]byte, error) {
	if !regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`).MatchString(key) || strings.ContainsAny(value, "\r\n") {
		return nil, fmt.Errorf("invalid environment value")
	}
	lines := strings.SplitAfter(string(source), "\n")
	prefix := key + "="
	for i, line := range lines {
		if strings.HasPrefix(line, prefix) {
			parts := environmentLine.FindStringSubmatch(line)
			if parts == nil {
				return nil, fmt.Errorf("%s must be a single-line environment assignment", key)
			}
			lines[i] = prefix + value + parts[2]
			return []byte(strings.Join(lines, "")), nil
		}
	}
	return nil, fmt.Errorf("missing %s environment assignment", key)
}

func ReplaceImage(source []byte, image string) ([]byte, error) {
	if _, e := domain.ImageMajor(image); e != nil {
		return nil, e
	}
	return replaceServiceImage(source, "app", image)
}
func replaceServiceImage(source []byte, service, image string) ([]byte, error) {
	node, e := serviceImageNode(source, service)
	if e != nil {
		return nil, e
	}
	lines := strings.SplitAfter(string(source), "\n")
	index := node.Line - 1
	if index < 0 || index >= len(lines) {
		return nil, fmt.Errorf("invalid image source position")
	}
	line := strings.TrimRight(lines[index], "\r\n")
	parts := scalarLine.FindStringSubmatch(line)
	if parts == nil {
		return nil, fmt.Errorf("the image must be an explicit single-line YAML value")
	}
	ending := lines[index][len(line):]
	lines[index] = parts[1] + image + parts[2] + ending
	return []byte(strings.Join(lines, "")), nil
}
func ImageChoices(target int) (map[string]string, error) {
	if target < 33 || target > 35 {
		return nil, fmt.Errorf("target Nextcloud major must be between 33 and 35")
	}
	out := map[string]string{}
	for major := 33; major <= target; major++ {
		out[fmt.Sprintf("%d", major)] = fmt.Sprintf("nextcloud:%d-apache", major)
	}
	return out, nil
}

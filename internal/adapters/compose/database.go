package compose

import (
	"fmt"
	"go.yaml.in/yaml/v3"
	"nextcloud-updater/internal/domain"
	"strings"
)

// ReplaceDatabase preserves the source outside the database image and the one
// explicitly required entrypoint setting. Complex inherited environments fail closed.
func ReplaceDatabase(source []byte, image string) ([]byte, error) {
	if _, err := domain.DatabaseImage(image); err != nil {
		return nil, err
	}
	b, err := replaceServiceImage(source, "db", image)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err = yaml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	services, err := child(doc.Content[0], "services")
	if err != nil {
		return nil, err
	}
	service, err := child(services, "db")
	if err != nil {
		return nil, err
	}
	environment, err := child(service, "environment")
	if err != nil {
		return nil, err
	}
	if environment.Style != 0 || (environment.Kind != yaml.MappingNode && environment.Kind != yaml.SequenceNode) || len(environment.Content) == 0 {
		return nil, fmt.Errorf("MariaDB requires an explicit block environment mapping/list")
	}
	lines := strings.SplitAfter(string(b), "\n")
	ending := "\n"
	if strings.Contains(string(b), "\r\n") {
		ending = "\r\n"
	}
	for i := 0; i < len(environment.Content); i++ {
		n := environment.Content[i]
		key := n.Value
		if environment.Kind == yaml.MappingNode {
			i++
			key = n.Value
		} else {
			key, _, _ = strings.Cut(key, "=")
		}
		if key == "<<" || n.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("MariaDB environment aliases/merge keys are unsupported")
		}
		if key == "MARIADB_AUTO_UPGRADE" {
			indent := strings.Repeat(" ", n.Column-1)
			line := indent + "MARIADB_AUTO_UPGRADE: \"1\"" + ending
			if environment.Kind == yaml.SequenceNode {
				indent = strings.Repeat(" ", environment.Column-1)
				line = indent + "- MARIADB_AUTO_UPGRADE=1" + ending
			}
			lines[n.Line-1] = line
			return []byte(strings.Join(lines, "")), nil
		}
	}
	first := environment.Content[0]
	indent := strings.Repeat(" ", first.Column-1)
	line := indent + "MARIADB_AUTO_UPGRADE: \"1\"" + ending
	if environment.Kind == yaml.SequenceNode {
		indent = strings.Repeat(" ", environment.Column-1)
		line = indent + "- MARIADB_AUTO_UPGRADE=1" + ending
	}
	index := first.Line - 1
	lines = append(lines[:index], append([]string{line}, lines[index:]...)...)
	return []byte(strings.Join(lines, "")), nil
}

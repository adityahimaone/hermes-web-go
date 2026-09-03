package skillsmem

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// SaveSkill writes (or overwrites) <home>/skills/[category/]<name>/SKILL.md with
// content, matching Python _handle_skill_save: lowercase name, reject slash or
// "..", resolve inside the skills root, refuse symlinked targets.
func SaveSkill(home, name, content, category string) (string, error) {
	name = strings.TrimSpace(name)
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "-")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, `\`) || strings.Contains(name, "..") {
		return "", ErrInvalidSkillName
	}
	category = strings.TrimSpace(category)
	if category != "" && (strings.Contains(category, "/") || strings.Contains(category, `\`) || strings.Contains(category, "..")) {
		return "", ErrInvalidSkillName
	}
	root := filepath.Clean(skillRoots(home)[0])
	dir := root
	if category != "" {
		dir = filepath.Join(dir, category)
	}
	dir = filepath.Join(dir, name)
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrInvalidSkillName
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	target := filepath.Join(dir, "SKILL.md")
	if fi, err := os.Lstat(target); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return "", ErrSymlinkedTarget
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return "", err
	}
	return target, nil
}

// DeleteSkill removes the skill directory that owns a SKILL.md whose parent
// dir or frontmatter name matches name, mirroring Python _handle_skill_delete.
func DeleteSkill(home, name string) error {
	name = strings.TrimSpace(name)
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "-")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, `\`) || strings.Contains(name, "..") {
		return ErrInvalidSkillName
	}
	path, err := FindSkillMD(home, name)
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Dir(path))
}

// readFileOrEmpty reads path or returns "" on any error.
func readFileOrEmpty(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// ToggleSkill flips name's presence in skills.disabled (and
// skills.platform_disabled.webui if present) of <home>/config.yaml, mirroring
// Python _handle_skill_toggle. disabled=true adds the name to the disabled
// list; disabled=false removes it — Python's _toggle_name_in_list treats the
// list as "disabled names" and enabled=True removes from it.
//
// Round-trips through yaml.Node so comments and key order survive, matching
// Python's ruamel.yaml round-trip semantics.
func ToggleSkill(home, name string, disabled bool) error {
	configPath := filepath.Join(home, "config.yaml")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return err
	}
	// root is the document node; its single child is the mapping.
	doc := root.Content[0]
	var setDisabled = func(listNode *yaml.Node) {
		var values []string
		if listNode != nil && listNode.Kind == yaml.SequenceNode {
			for _, item := range listNode.Content {
				values = append(values, item.Value)
			}
		}
		found := false
		for _, v := range values {
			if v == name {
				found = true
				break
			}
		}
		if disabled && !found {
			values = append(values, name)
		}
		if !disabled && found {
			kept := values[:0]
			for _, v := range values {
				if v != name {
					kept = append(kept, v)
				}
			}
			values = kept
		}
		if listNode == nil {
			seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			for _, v := range values {
				seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v})
			}
			configNode := findMapKey(doc, "skills")
			if configNode == nil {
				configNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				doc.Content = append(doc.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "skills"}, configNode)
			}
			configNode.Content = append(configNode.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "disabled"}, seq)
			return
		}
		listNode.Content = listNode.Content[:0]
		for _, v := range values {
			listNode.Content = append(listNode.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v})
		}
	}
	skillsNode := findMapKey(doc, "skills")
	if skillsNode == nil {
		setDisabled(nil)
	} else {
		setDisabled(findMapKey(skillsNode, "disabled"))
		// platform_disabled.webui, if present, mirrors the same change.
		if platNode := findMapKey(skillsNode, "platform_disabled"); platNode != nil {
			if webuiNode := findMapKey(platNode, "webui"); webuiNode != nil {
				setDisabledForList(webuiNode, name, disabled)
			}
		}
	}
	out, err := yaml.Marshal(&root)
	if err != nil {
		return err
	}
	tmp := configPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, configPath)
}

func setDisabledForList(listNode *yaml.Node, name string, disabled bool) {
	var values []string
	if listNode != nil && listNode.Kind == yaml.SequenceNode {
		for _, item := range listNode.Content {
			values = append(values, item.Value)
		}
	}
	found := false
	for _, v := range values {
		if v == name {
			found = true
			break
		}
	}
	if disabled && !found {
		values = append(values, name)
	}
	if !disabled && found {
		kept := values[:0]
		for _, v := range values {
			if v != name {
				kept = append(kept, v)
			}
		}
		values = kept
	}
	listNode.Content = listNode.Content[:0]
	for _, v := range values {
		listNode.Content = append(listNode.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v})
	}
}

// findMapKey returns the value node for key under a mapping node, or nil.
func findMapKey(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

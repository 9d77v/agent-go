package skills

import (
	"sort"
	"strings"
	"sync"
)

type Registry struct {
	mu     sync.RWMutex
	skills map[string]*Skill
}

var globalRegistry *Registry
var once sync.Once

func Global() *Registry {
	once.Do(func() {
		globalRegistry = &Registry{skills: make(map[string]*Skill)}
	})
	return globalRegistry
}

func (r *Registry) Register(skill *Skill) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if skill == nil || skill.Meta.Name == "" {
		return
	}
	r.skills[skill.Meta.Name] = skill
}

func (r *Registry) Get(name string) *Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.skills[name]
}

func (r *Registry) List() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*Skill, 0, len(r.skills))
	for _, s := range r.skills {
		result = append(result, s)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Meta.Name < result[j].Meta.Name })
	return result
}

func (r *Registry) ListInvocable() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []*Skill
	for _, s := range r.skills {
		if !s.Meta.DisableModelInvocation {
			result = append(result, s)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Meta.Name < result[j].Meta.Name })
	return result
}

func (r *Registry) ListBySource(source SkillSource) []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []*Skill
	for _, s := range r.skills {
		if s.Source == source {
			result = append(result, s)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Meta.Name < result[j].Meta.Name })
	return result
}

func (r *Registry) ListBySources(sources ...SkillSource) []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := make(map[string]bool)
	var result []*Skill
	for _, source := range sources {
		for _, s := range r.skills {
			if s.Source == source && !seen[s.Meta.Name] {
				seen[s.Meta.Name] = true
				result = append(result, s)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Meta.Name < result[j].Meta.Name })
	return result
}

func (r *Registry) ListEnabled() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []*Skill
	for _, s := range r.skills {
		if s.Meta.Enabled {
			result = append(result, s)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Meta.Name < result[j].Meta.Name })
	return result
}

func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.skills, name)
}

func (r *Registry) ClearSource(source SkillSource) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, s := range r.skills {
		if s.Source == source {
			delete(r.skills, name)
		}
	}
}

func ParseFrontmatter(content string) (SkillMeta, string, string) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return SkillMeta{}, content, content
	}
	rest := content[3:]
	before, after, ok := strings.Cut(rest, "\n---")
	if !ok {
		return SkillMeta{}, content, content
	}
	frontmatterStr := strings.TrimSpace(before)
	body := strings.TrimSpace(after)
	body = strings.TrimPrefix(body, "\n")
	meta := parseYAMLFrontmatter(frontmatterStr)
	return meta, content, body
}

func parseYAMLFrontmatter(s string) SkillMeta {
	var meta SkillMeta
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch key {
		case "name":
			meta.Name = val
		case "description":
			meta.Description = val
		case "argument-hint":
			meta.ArgumentHint = val
		case "user-invocable":
			meta.UserInvocable = val == "true"
		case "disable-model-invocation":
			meta.DisableModelInvocation = val == "true"
		case "enabled":
			meta.Enabled = val != "false"
		}
	}
	return meta
}

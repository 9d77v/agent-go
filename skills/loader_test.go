package skills

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRegisterAndGet(t *testing.T) {
	r := Global()
	r.Register(&Skill{Meta: SkillMeta{Name: "test-skill"}, Body: "content", Source: SourceBuiltin})
	skill := r.Get("test-skill")
	assert.NotNil(t, skill)
	assert.Equal(t, "test-skill", skill.Meta.Name)
	assert.Equal(t, "content", skill.Body)
}

func TestListEnabled(t *testing.T) {
	r := Global()
	r.Register(&Skill{Meta: SkillMeta{Name: "enabled-skill", Enabled: true}, Source: SourceBuiltin})
	r.Register(&Skill{Meta: SkillMeta{Name: "disabled-skill", Enabled: false}, Source: SourceBuiltin})
	enabled := r.ListEnabled()
	for _, s := range enabled {
		assert.True(t, s.Meta.Enabled)
	}
}

func TestListInvocable(t *testing.T) {
	r := Global()
	r.Register(&Skill{Meta: SkillMeta{Name: "invocable", DisableModelInvocation: false}, Source: SourceBuiltin})
	r.Register(&Skill{Meta: SkillMeta{Name: "not-invocable", DisableModelInvocation: true}, Source: SourceBuiltin})
	invocable := r.ListInvocable()
	for _, s := range invocable {
		assert.False(t, s.Meta.DisableModelInvocation)
	}
}

func TestClearSource(t *testing.T) {
	r := Global()
	r.Register(&Skill{Meta: SkillMeta{Name: "clear-test"}, Source: SourceCustom})
	assert.NotNil(t, r.Get("clear-test"))
	r.ClearSource(SourceCustom)
	assert.Nil(t, r.Get("clear-test"))
}

func TestParseFrontmatter(t *testing.T) {
	input := `---
name: my-skill
description: A test skill
enabled: true
---
This is the skill body.`
	meta, content, body := ParseFrontmatter(input)
	assert.Equal(t, "my-skill", meta.Name)
	assert.Equal(t, "A test skill", meta.Description)
	assert.True(t, meta.Enabled)
	assert.Contains(t, body, "This is the skill body.")
	assert.Equal(t, input, content)
}

func TestParseFrontmatterNoFrontmatter(t *testing.T) {
	input := "Just body content without frontmatter"
	meta, content, body := ParseFrontmatter(input)
	assert.Equal(t, "", meta.Name)
	assert.Equal(t, input, content)
	assert.Equal(t, input, body)
}

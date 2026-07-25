package skills

type SkillSource string

const (
	SourceBuiltin   SkillSource = "builtin"
	SourceWorkspace SkillSource = "workspace"
	SourceCustom    SkillSource = "custom"
)

type SkillMeta struct {
	Name                   string `yaml:"name"`
	Description            string `yaml:"description"`
	ArgumentHint           string `yaml:"argument-hint"`
	UserInvocable          bool   `yaml:"user-invocable"`
	DisableModelInvocation bool   `yaml:"disable-model-invocation"`
	Enabled                bool   `yaml:"enabled"`
}

type Skill struct {
	Meta     SkillMeta
	Content  string
	Body     string
	Workflow string
	Source   SkillSource
	BuiltIn  bool
	DBID     string
}

type SkillSummary struct {
	Name                   string      `json:"name"`
	Description            string      `json:"description"`
	ArgumentHint           string      `json:"argument_hint"`
	UserInvocable          bool        `json:"user_invocable"`
	DisableModelInvocation bool        `json:"disable_model_invocation"`
	BuiltIn                bool        `json:"built_in"`
	Source                 SkillSource `json:"source"`
	Enabled                bool        `json:"enabled"`
}

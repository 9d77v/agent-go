package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	adktool "google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/toolutils"
)

// ArtifactLoadHandler 决定如何把制品 part 转换为可注入模型的 part。
// 典型实现：
//   - 主模型支持视觉（Chat API）→ 返回原始图片 part（InlineData），由 ChatModel 转 image_url 原生看图；
//   - 主模型不支持视觉 / Responses API → 调用 VL 模型返回文字描述 part。
type ArtifactLoadHandler func(ctx context.Context, artifactName string, part *genai.Part) (*genai.Part, error)

// LoadArtifactsTool 是 load_artifacts 工具的自定义实现。
// 模型通过调用 load_artifacts 按需加载上传的图片/文件制品（类比 read_file 读取文件），
// 加载内容经 handler 转换后以 user 角色追加到下一次 LLM 请求。
type LoadArtifactsTool struct {
	handler ArtifactLoadHandler
}

// NewLoadArtifactsTool 创建 load_artifacts 工具。
func NewLoadArtifactsTool(handler ArtifactLoadHandler) adktool.Tool {
	return &LoadArtifactsTool{handler: handler}
}

func (t *LoadArtifactsTool) Name() string { return "load_artifacts" }
func (t *LoadArtifactsTool) Description() string {
	return "Loads the uploaded image/file artifacts and adds their content to the session."
}
func (t *LoadArtifactsTool) IsLongRunning() bool { return false }

// Declaration 返回 load_artifacts 的函数声明。
func (t *LoadArtifactsTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"artifact_names": {
					Type:  genai.TypeArray,
					Items: &genai.Schema{Type: genai.TypeString},
				},
			},
			Required: []string{"artifact_names"},
		},
	}
}

// Run 回显请求的制品名（结果作为 FunctionResponse 存入会话，实际加载在 ProcessRequest 完成）。
func (t *LoadArtifactsTool) Run(ctx agent.Context, args any) (map[string]any, error) {
	m, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected args type: %T", args)
	}
	var names []string
	raw, exists := m["artifact_names"]
	if exists {
		if b, err := json.Marshal(raw); err == nil {
			_ = json.Unmarshal(b, &names)
		}
	}
	if names == nil {
		names = []string{}
	}
	return map[string]any{"artifact_names": names}, nil
}

// ProcessRequest 在每次模型调用前执行：
// 1. 打包工具声明；
// 2. 存在制品时追加"应调用 load_artifacts"指令；
// 3. 检测到 load_artifacts 的 FunctionResponse 时，逐个加载制品并经 handler 转换后追加到 req.Contents。
func (t *LoadArtifactsTool) ProcessRequest(ctx agent.Context, req *model.LLMRequest) error {
	if err := toolutils.PackTool(req, t); err != nil {
		return err
	}
	if err := t.appendInitialInstructions(ctx, req); err != nil {
		return err
	}
	return t.processLoadFunctionCall(ctx, req)
}

// appendInitialInstructions 列出会话内制品，并告知模型如何加载。
func (t *LoadArtifactsTool) appendInitialInstructions(ctx agent.Context, req *model.LLMRequest) error {
	arts := ctx.Artifacts()
	if arts == nil {
		return nil
	}
	resp, err := arts.List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list artifacts: %w", err)
	}
	if len(resp.FileNames) == 0 {
		return nil
	}
	namesJSON, _ := json.Marshal(resp.FileNames)
	inst := fmt.Sprintf(
		"You have a list of uploaded artifacts:\n  %s\n\nWhen the user asks questions about any of the artifacts, you should call the `load_artifacts` function to load the artifact. Always load an artifact to access its content, even if it has been loaded before.",
		string(namesJSON))
	appendInstructions(req, inst)
	return nil
}

// processLoadFunctionCall 处理模型发起的 load_artifacts 调用。
func (t *LoadArtifactsTool) processLoadFunctionCall(ctx agent.Context, req *model.LLMRequest) error {
	arts := ctx.Artifacts()
	if arts == nil || len(req.Contents) == 0 {
		return nil
	}
	lastContent := req.Contents[len(req.Contents)-1]
	if lastContent == nil || len(lastContent.Parts) == 0 {
		return nil
	}
	fr := lastContent.Parts[0].FunctionResponse
	if fr == nil || fr.Name != "load_artifacts" {
		return nil
	}

	var names []string
	switch v := fr.Response["artifact_names"].(type) {
	case []string:
		names = v
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				names = append(names, s)
			}
		}
	}
	if len(names) == 0 {
		return nil
	}

	results := make([]*genai.Content, len(names))
	for i, name := range names {
		lr, err := arts.Load(ctx, name)
		if err != nil {
			return fmt.Errorf("failed to load artifact %s: %w", name, err)
		}
		if lr == nil || lr.Part == nil {
			return fmt.Errorf("artifact %s is empty", name)
		}
		part, err := t.handler(ctx, name, lr.Part)
		if err != nil {
			return fmt.Errorf("failed to process artifact %s: %w", name, err)
		}
		results[i] = &genai.Content{
			Parts: []*genai.Part{
				genai.NewPartFromText("Artifact " + name + " is:"),
				part,
			},
			Role: genai.RoleUser,
		}
	}
	req.Contents = append(req.Contents, results...)
	return nil
}

// appendInstructions 复刻 ADK internal/utils.AppendInstructions（agent-go 无法 import internal 包）。
func appendInstructions(r *model.LLMRequest, instructions ...string) {
	if len(instructions) == 0 {
		return
	}
	inst := strings.Join(instructions, "\n\n")
	if r.Config == nil {
		r.Config = &genai.GenerateContentConfig{}
	}
	if r.Config.SystemInstruction == nil {
		r.Config.SystemInstruction = genai.NewContentFromText(inst, genai.RoleUser)
		return
	}
	if n := len(r.Config.SystemInstruction.Parts); n > 0 && r.Config.SystemInstruction.Parts[n-1].Text != "" {
		r.Config.SystemInstruction.Parts[n-1].Text += "\n\n" + inst
		return
	}
	r.Config.SystemInstruction.Parts = append(r.Config.SystemInstruction.Parts, genai.NewPartFromText(inst))
}

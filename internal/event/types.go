package event

type Kind string

const (
	KindUser      Kind = "user"
	KindAssistant Kind = "assistant"
	KindTool      Kind = "tool"
	KindDiff      Kind = "diff"
)

type Event struct {
	Kind     Kind   `json:"kind"`
	Text     string `json:"text"`
	ToolName string `json:"tool_name,omitempty"`
	File     string `json:"file,omitempty"`
	Ts       int64  `json:"ts"`
}

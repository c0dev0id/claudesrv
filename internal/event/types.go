package event

type Kind string

const (
	KindUser      Kind = "user"
	KindAssistant Kind = "assistant"
	KindTool      Kind = "tool"
)

type Event struct {
	Kind     Kind   `json:"kind"`
	Text     string `json:"text"`
	ToolName string `json:"tool_name,omitempty"`
	Ts       int64  `json:"ts"`
}

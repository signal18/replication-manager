package shared

// State Levels
const (
	StateWarn = "WARNING"
	StateErr  = "ERROR"
)

// Log message
// in: Message
// swagger:response message
type Message struct {
	Group     string
	Level     string
	Timestamp string
	Text      string
	Module    int
	Fields    map[string]interface{}
}

func FromLogrusLevel(level uint32) string {
	switch level {
	case 5:
		return "DEBUG"
	case 4:
		return "INFO"
	case 3:
		return "WARN"
	default:
		return "ERROR"
	}
}

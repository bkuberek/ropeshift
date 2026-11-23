package api

// This tree breaks the rule: a caller outside the control plane records an answer without a question.
func send(m interface {
	RecordAnswer()
}) {
	m.RecordAnswer()
}

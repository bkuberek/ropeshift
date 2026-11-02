package contract

import (
	"os"
	"slices"
)

// AllChecks lists every invariant of the suite.
func AllChecks() []Check {
	return slices.Concat(StoreChecks, LeaseChecks, IntakeChecks, LaneChecks, GenerationChecks, QuestionChecks, BatchChecks, DecisionChecks, AcceptanceChecks, WakeChecks, ParkChecks, ResumeChecks, MessageChecks, AnswerChecks, LimitChecks)
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

package contract

import (
	"os"
	"slices"
)

// AllChecks lists every invariant of the suite.
func AllChecks() []Check {
	return slices.Concat(StoreChecks, LeaseChecks, IntakeChecks, LaneChecks, GenerationChecks, ApprovalChecks, BlockedChecks, QuestionChecks, BatchChecks, DecisionChecks, AcceptanceChecks, WakeChecks, ParkChecks, AutonomyChecks, PlanLaneChecks, ResumeChecks, MessageChecks, AnswerChecks, LimitChecks, PackChecks, NoticeChecks)
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

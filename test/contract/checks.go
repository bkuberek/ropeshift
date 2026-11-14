package contract

import (
	"os"
	"slices"
)

// AllChecks lists every invariant of the suite.
func AllChecks() []Check {
	return slices.Concat(StoreChecks, LeaseChecks, CancelChecks, NotifierChecks, IntakeChecks, LaneChecks, GenerationChecks, ApprovalChecks, EffectChecks, BlockedChecks, QuestionChecks, BatchChecks, DecisionChecks, ExperienceChecks, AcceptanceChecks, WakeChecks, ParkChecks, AutonomyChecks, PlanLaneChecks, ResumeChecks, MessageChecks, AnswerChecks, LimitChecks, PackChecks, NoticeChecks, DocumentChecks, DocumentPackChecks, TraceChecks, MilestoneChecks, PlanApprovalChecks, RepositoryChecks, StartChecks, ContinueChecks, ReadChecks, OrderChecks, AdoptionChecks, AdoptChecks, ReviewWorkChecks, VerdictChecks, VerdictAcceptanceChecks, CalibrationChecks, RecoveryChecks, ForgeChecks, ForgeMergeChecks, MergeChecks, PlanMergeChecks, RosterChecks, DelegateChecks, ConversationChecks, DeliveryChecks)
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

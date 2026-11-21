package contract

import (
	"os"
	"slices"
)

// AllChecks lists every invariant of the suite.
func AllChecks() []Check {
	return slices.Concat(StoreChecks, LeaseChecks, CancelChecks, NotifierChecks, SpendNoticeChecks, IntakeChecks, LaneChecks, GenerationChecks, ApprovalChecks, EffectChecks, BlockedChecks, QuestionChecks, BatchChecks, BatchAnswerChecks, DecisionChecks, ExperienceChecks, AcceptanceChecks, WakeChecks, ParkChecks, AutonomyChecks, OrchestratorEnrollChecks, PlanLaneChecks, ResumeChecks, MessageChecks, AnswerChecks, LimitChecks, PackChecks, NoticeChecks, DocumentChecks, DocumentPackChecks, TraceChecks, MilestoneChecks, PlanApprovalChecks, RepositoryChecks, StartChecks, ContinueChecks, ReadChecks, OrderChecks, ManifestChecks, ManifestCreateChecks, ManifestInsideChecks, WorktreeRootChecks, AdoptionChecks, AdoptChecks, ReviewWorkChecks, VerdictChecks, VerdictAcceptanceChecks, CalibrationChecks, RecoveryChecks, SpendChecks, ChargedSpendChecks, HardStopChecks, SpendStopChecks, ProjectLimitChecks, ProjectHoldChecks, CIChecks, CIHistoryChecks, CIAttentionChecks, CIClassifyChecks, ForgeChecks, ForgeMergeChecks, MergeChecks, PlanMergeChecks, RosterChecks, DelegateChecks, ConversationChecks, DeliveryChecks, EstimateChecks, ForecastChecks, BaselineChecks, OutcomeChecks, PlanningViewChecks, MidPlanChecks, UnjudgedChecks, OverlapChecks, WholeLadderChecks, ScheduledStartStoreChecks, ScheduledStartChecks, BindingChecks, ByIDChecks, RevisionChecks, JoinTokenChecks, SupersedeChecks, DigestChecks, PracticesChecks, PracticesNeverBlockChecks)
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

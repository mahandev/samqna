package model

type SubmissionStatus string

const (
	StatusProcessing  SubmissionStatus = "processing"
	StatusReady       SubmissionStatus = "ready"
	StatusQuarantined SubmissionStatus = "quarantined"
	StatusFailed      SubmissionStatus = "failed"
)

type JobStage string

const (
	StageExtract    JobStage = "extract"
	StageTranscribe JobStage = "transcribe"
	StageTagGrade   JobStage = "tag_grade"
	StageDone       JobStage = "done"
)

type JobStatus string

const (
	JobPending JobStatus = "pending"
	JobRunning JobStatus = "running"
	JobFailed  JobStatus = "failed"
)

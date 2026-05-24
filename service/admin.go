package service

import (
	"samqna/model"
	"samqna/repository"
)

type Admin struct {
	Subs *repository.SubmissionRepo
	Jobs *repository.JobRepo
	IPs  *repository.IPRepo
}

func (a *Admin) ToggleStar(id string) error {
	s, err := a.Subs.Get(id)
	if err != nil { return err }
	s.Starred = !s.Starred
	return a.Subs.Update(s)
}

func (a *Admin) Delete(id string) error          { return a.Subs.SoftDelete(id) }
func (a *Admin) BlockIP(ip, reason string) error { return a.IPs.Block(ip, reason) }

func (a *Admin) Quarantine(id string, on bool) error {
	s, err := a.Subs.Get(id)
	if err != nil { return err }
	if on { s.Status = model.StatusQuarantined } else { s.Status = model.StatusReady }
	return a.Subs.Update(s)
}

func (a *Admin) Requeue(id string) error {
	job, err := a.Jobs.GetBySubmission(id)
	if err != nil { return err }
	return a.Jobs.AdvanceStage(job.ID, model.StageExtract)
}

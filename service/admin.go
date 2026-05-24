package service

import (
	"encoding/json"
	"fmt"

	"samqna/model"
	"samqna/repository"

	"gorm.io/gorm"
)

// Admin is the only place destructive admin actions are mutated. Every
// mutating method writes to AuditRepo on success and (if configured) pings
// the Notifier so the creator gets a real-time alert on their phone.
//
// The DB handle is exposed because tag editing uses GORM's many2many
// association helper, which isn't expressed in the repo layer.
type Admin struct {
	DB       *gorm.DB
	Subs     *repository.SubmissionRepo
	Jobs     *repository.JobRepo
	Tags     *repository.TagRepo
	IPs      *repository.IPRepo
	Audits   *repository.AuditRepo
	Settings *repository.SettingsRepo
	Notify   func(msg string) // optional; safe-no-op when nil
}

func (a *Admin) audit(actor, action, target, meta string) {
	if a.Audits == nil {
		return
	}
	_ = a.Audits.Write(actor, action, target, meta)
	if a.Notify != nil {
		a.Notify(fmt.Sprintf("admin: %s %s %s", actor, action, target))
	}
}

func (a *Admin) ToggleStar(actor, id string) error {
	s, err := a.Subs.Get(id)
	if err != nil {
		return err
	}
	s.Starred = !s.Starred
	if err := a.Subs.Update(s); err != nil {
		return err
	}
	state := "off"
	if s.Starred {
		state = "on"
	}
	a.audit(actor, "star_"+state, id, "")
	return nil
}

func (a *Admin) Delete(actor, id string) error {
	if err := a.Subs.SoftDelete(id); err != nil {
		return err
	}
	a.audit(actor, "delete", id, "")
	return nil
}

func (a *Admin) BlockIP(actor, ip, reason string) error {
	if err := a.IPs.Block(ip, reason); err != nil {
		return err
	}
	meta, _ := json.Marshal(map[string]string{"reason": reason})
	a.audit(actor, "block_ip", ip, string(meta))
	return nil
}

func (a *Admin) UnblockIP(actor, ip string) error {
	if err := a.IPs.Unblock(ip); err != nil {
		return err
	}
	a.audit(actor, "unblock_ip", ip, "")
	return nil
}

func (a *Admin) Quarantine(actor, id string, on bool) error {
	s, err := a.Subs.Get(id)
	if err != nil {
		return err
	}
	if on {
		s.Status = model.StatusQuarantined
	} else {
		s.Status = model.StatusReady
	}
	if err := a.Subs.Update(s); err != nil {
		return err
	}
	action := "quarantine_on"
	if !on {
		action = "quarantine_off"
	}
	a.audit(actor, action, id, "")
	return nil
}

func (a *Admin) Requeue(actor, id string) error {
	job, err := a.Jobs.GetBySubmission(id)
	if err != nil {
		return err
	}
	if err := a.Jobs.AdvanceStage(job.ID, model.StageExtract); err != nil {
		return err
	}
	a.audit(actor, "requeue", id, "")
	return nil
}

// EditTags replaces all tags on a submission. Uses GORM's many2many
// Association().Replace() helper, which inserts new tags and removes
// stale ones in one shot. Tag names are canonicalized first.
func (a *Admin) EditTags(actor, id string, names []string) error {
	sub, err := a.Subs.Get(id)
	if err != nil {
		return err
	}
	tags, err := a.Tags.GetOrCreate(names)
	if err != nil {
		return err
	}
	if err := a.DB.Model(sub).Association("Tags").Replace(tags); err != nil {
		return err
	}
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		out = append(out, t.Name)
	}
	meta, _ := json.Marshal(map[string]any{"tags": out})
	a.audit(actor, "tag_edit", id, string(meta))
	return nil
}

// Pause sets the global submissions-paused flag. While true, new uploads
// are rejected at the HTTP layer with 503.
func (a *Admin) Pause(actor string) error {
	if err := a.Settings.SetBool(repository.KeySubmissionsPaused, true); err != nil {
		return err
	}
	a.audit(actor, "pause", "", "")
	return nil
}

func (a *Admin) Unpause(actor string) error {
	if err := a.Settings.SetBool(repository.KeySubmissionsPaused, false); err != nil {
		return err
	}
	a.audit(actor, "unpause", "", "")
	return nil
}

// IsPaused is non-mutating; safe to call from the public submit path.
func (a *Admin) IsPaused() bool {
	if a.Settings == nil {
		return false
	}
	v, _ := a.Settings.Bool(repository.KeySubmissionsPaused)
	return v
}

// RecentAudits is for the /admin dashboard.
func (a *Admin) RecentAudits(limit int) ([]model.AdminAudit, error) {
	if a.Audits == nil {
		return nil, nil
	}
	return a.Audits.Recent(limit)
}

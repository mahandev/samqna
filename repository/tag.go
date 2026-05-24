package repository

import (
	"regexp"
	"strings"

	"samqna/model"

	"gorm.io/gorm"
)

type TagRepo struct {
	DB *gorm.DB
}

func NewTagRepo(db *gorm.DB) *TagRepo {
	return &TagRepo{DB: db}
}

var nonTagChars = regexp.MustCompile(`[^a-z0-9\-]+`)

// Canonicalize lowercases, strips punctuation, replaces spaces with '-', dedupes.
func Canonicalize(names []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, n := range names {
		c := strings.ToLower(strings.TrimSpace(n))
		c = strings.ReplaceAll(c, " ", "-")
		c = nonTagChars.ReplaceAllString(c, "")
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

func (r *TagRepo) GetOrCreate(names []string) ([]model.Tag, error) {
	canon := Canonicalize(names)
	tags := make([]model.Tag, 0, len(canon))
	for _, n := range canon {
		var t model.Tag
		if err := r.DB.Where("name = ?", n).FirstOrCreate(&t, model.Tag{Name: n}).Error; err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, nil
}

func (r *TagRepo) AllWithCounts() (map[string]int64, error) {
	type row struct {
		Name  string
		Count int64
	}
	var rows []row
	err := r.DB.Raw(`
		SELECT t.name AS name, COUNT(*) AS count
		FROM tags t
		JOIN submission_tags st ON st.tag_id = t.id
		JOIN submissions s ON s.id = st.submission_id
		WHERE s.status = 'ready' AND s.deleted_at IS NULL
		GROUP BY t.name
		ORDER BY count DESC
	`).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, r := range rows {
		out[r.Name] = r.Count
	}
	return out, nil
}

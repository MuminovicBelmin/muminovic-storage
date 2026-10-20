// Package domain contains update rules and values, without SSH or Docker dependencies.
package domain

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type Version [3]int

var release = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:\.\d+)?$`)
var imageTag = regexp.MustCompile(`^nextcloud:(\d+)(?:-apache)?$`)

func ParseVersion(s string) (Version, error) {
	m := release.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("unrecognized Nextcloud release")
	}
	var v Version
	for i := range v {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return v, err
		}
		v[i] = n
	}
	return v, nil
}
func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]) }
func (v Version) Before(other Version) bool {
	for i := range v {
		if v[i] != other[i] {
			return v[i] < other[i]
		}
	}
	return false
}
func ImageMajor(image string) (int, error) {
	m := imageTag.FindStringSubmatch(image)
	if m == nil {
		return 0, fmt.Errorf("only official nextcloud:<major> or nextcloud:<major>-apache image tags are allowed")
	}
	return strconv.Atoi(m[1])
}
func ValidateStep(current, target Version, major int) error {
	if major != current[0] && major != current[0]+1 {
		return fmt.Errorf("skipping major versions is prohibited")
	}
	if target[0] != major || target.Before(current) {
		return fmt.Errorf("the image would downgrade or select the wrong major")
	}
	return nil
}

type Phase string

const (
	Planning          Phase = "planning"
	Ready             Phase = "ready"
	Prepared          Phase = "prepared"
	Quiescing         Phase = "quiescing"
	BackingUp         Phase = "backing_up"
	BackedUp          Phase = "backed_up"
	DeploymentStarted Phase = "deployment_started"
	Verified          Phase = "verified"
	Closed            Phase = "closed"
)

func RequireApproval(action string, approved bool, phase Phase) error {
	mutating := action == "prepare" || action == "backup" || action == "deploy"
	if action == "recover" {
		mutating = phase == Quiescing || phase == BackingUp || phase == BackedUp || phase == DeploymentStarted
	}
	if mutating && !approved {
		return fmt.Errorf("explicit --approve-update is required")
	}
	return nil
}

type Status struct {
	Installed      bool   `json:"installed"`
	Maintenance    bool   `json:"maintenance"`
	NeedsDBUpgrade bool   `json:"needsDbUpgrade"`
	Version        string `json:"version"`
}

func (s Status) Healthy() bool { return s.Installed && !s.Maintenance && !s.NeedsDBUpgrade }

type Deployment struct {
	Root          string   `json:"root"`
	Project       string   `json:"project"`
	Source        string   `json:"source"`
	Environment   string   `json:"environment"`
	Compose       []string `json:"compose"`
	DatabaseID    string   `json:"database_id"`
	DatabaseImage string   `json:"database_image"`
	DatabaseName  string   `json:"database_name"`
}
type Image struct {
	Tag     string `json:"tag"`
	ID      string `json:"id"`
	Version string `json:"version"`
}
type ArchivePath struct {
	Source  string `json:"source"`
	Archive string `json:"archive"`
}
type BackupInput struct {
	Kind                   string
	Paths                  []ArchivePath
	Metadata               any
	EstimatedDatabaseBytes int64
}
type Artifact struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type Manifest struct {
	Kind          string     `json:"kind,omitempty"`
	FromVersion   string     `json:"from_version"`
	TargetVersion string     `json:"target_version"`
	Scope         string     `json:"scope"`
	Artifacts     []Artifact `json:"artifacts"`
}

const BackupScope = "all MariaDB databases (including mysql system tables/accounts), config, custom_apps, themes and encryption keys; S3 objects excluded"

type Backup struct {
	Directory string   `json:"remote_stage"`
	Manifest  Manifest `json:"manifest"`
	SHA256    string   `json:"manifest_sha256"`
}
type State struct {
	Kind        string     `json:"kind,omitempty"`
	Phase       Phase      `json:"phase"`
	Deployment  Deployment `json:"deployment"`
	Stage       string     `json:"stage"`
	FromVersion string     `json:"from_version"`
	Target      Image      `json:"target"`
	SourceHash  string     `json:"source_hash"`
	EnabledApps []string   `json:"enabled_apps"`
	Backup      Backup     `json:"backup"`
}
type Plan struct {
	DatabaseVersion        string   `json:"database_version"`
	DatabaseBefore         []string `json:"database_before"`
	DatabaseAfter          []string `json:"database_after"`
	CurrentVersion         string   `json:"current_version"`
	Stages                 []int    `json:"stages"`
	Scope                  string   `json:"scope"`
	EstimatedDatabaseBytes int64    `json:"estimated_database_bytes"`
}
type Step struct {
	Skip          bool   `json:"skip"`
	FromVersion   string `json:"from_version"`
	TargetVersion string `json:"target_version"`
}
type Result struct {
	DatabaseVersion string   `json:"database_version,omitempty"`
	Version         string   `json:"version"`
	DisabledApps    []string `json:"disabled_apps"`
	ComposeFile     string   `json:"compose_file"`
}
type Recovery struct {
	Recovered bool   `json:"recovered"`
	Message   string `json:"message"`
}
type Job struct {
	ID    any    `json:"id"`
	Class string `json:"class"`
}

func MigrationJobs(rows []Job) ([]Job, error) {
	if len(rows) >= 100000 {
		return nil, fmt.Errorf("background-job list is truncated")
	}
	var jobs []Job
	for _, j := range rows {
		c := strings.ToLower(j.Class)
		if strings.Contains(c, "migrat") || strings.Contains(c, "asyncrepair") {
			jobs = append(jobs, j)
		}
	}
	return jobs, nil
}

package domain

import "testing"

func TestInternalBuildVersionAndUpgradeRules(t *testing.T) {
	v, e := ParseVersion("30.0.8.1")
	if e != nil || v != (Version{30, 0, 8}) {
		t.Fatalf("version: %v %v", v, e)
	}
	for _, s := range []string{"latest", "35.0.0rc1", "-1.0.0"} {
		if _, e := ParseVersion(s); e == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	for _, tc := range []struct {
		target Version
		major  int
		ok     bool
	}{{Version{30, 0, 17}, 30, true}, {Version{31, 0, 14}, 31, true}, {Version{32, 0, 15}, 32, false}, {Version{30, 0, 7}, 30, false}, {Version{35, 0, 2}, 31, false}} {
		e := ValidateStep(v, tc.target, tc.major)
		if (e == nil) != tc.ok {
			t.Errorf("step %v: %v", tc, e)
		}
	}
}
func TestApprovalRequiredForServiceChanges(t *testing.T) {
	for _, action := range []string{"prepare", "backup", "deploy"} {
		if RequireApproval(action, false, Ready) == nil {
			t.Fatal("missing approval gate", action)
		}
		if e := RequireApproval(action, true, Ready); e != nil {
			t.Fatal(e)
		}
	}
	for _, phase := range []Phase{Quiescing, BackingUp, BackedUp, DeploymentStarted} {
		if RequireApproval("recover", false, phase) == nil {
			t.Fatal("recovery changed service without approval")
		}
	}
	if e := RequireApproval("recover", false, Planning); e != nil {
		t.Fatal(e)
	}
}
func TestOnlyOfficialMajorTags(t *testing.T) {
	for _, s := range []string{"nextcloud:stable", "other:31", "nextcloud:31;bad"} {
		if _, e := ImageMajor(s); e == nil {
			t.Fatal("accepted", s)
		}
	}
	if n, e := ImageMajor("nextcloud:31"); e != nil || n != 31 {
		t.Fatal(n, e)
	}
}
func TestMigrationSelectionDoesNotRunRetention(t *testing.T) {
	rows := []Job{{ID: 1, Class: `OCA\Files_Trashbin\BackgroundJob\ExpireTrash`}, {ID: 2, Class: `OC\Repair\AsyncRepair`}, {ID: 3, Class: `OCA\App\MigrateMetadata`}}
	jobs, e := MigrationJobs(rows)
	if e != nil || len(jobs) != 2 {
		t.Fatal(jobs, e)
	}
	if jobs[0].ID != 2 || jobs[1].ID != 3 {
		t.Fatal(jobs)
	}
}

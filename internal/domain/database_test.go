package domain

import (
	"reflect"
	"testing"
)

func TestDatabasePathKeepsOlderNextcloudOnCompatibleLTS(t *testing.T) {
	before, after, err := DatabasePath(Version{10, 6, 22}, 30, 35)
	if err != nil || !reflect.DeepEqual(before, []string{"10.11", "11.4"}) || !reflect.DeepEqual(after, []string{"11.8"}) {
		t.Fatal(before, after, err)
	}
	before, after, err = DatabasePath(Version{11, 4, 9}, 30, 31)
	if err != nil || !reflect.DeepEqual(before, []string{"11.4"}) || len(after) != 0 {
		t.Fatal(before, after, err)
	}
	if _, _, err = DatabasePath(Version{12, 3, 3}, 35, 35); err == nil {
		t.Fatal("implicit downgrade allowed")
	}
}
func TestDatabaseVersionsAndStepSafety(t *testing.T) {
	if DatabaseCompatible(Version{11, 8, 9}, 32) || !DatabaseCompatible(Version{11, 8, 9}, 33) {
		t.Fatal("MariaDB compatibility boundary is incorrect")
	}
	v, err := DatabaseVersion("11.4.9-MariaDB-ubu2404")
	if err != nil || v != (Version{11, 4, 9}) {
		t.Fatal(v, err)
	}
	for _, raw := range []string{"8.4.1", "not-a-version-MariaDB"} {
		if _, err := DatabaseVersion(raw); err == nil {
			t.Fatal(raw)
		}
	}
	for _, pair := range [][2]Version{{{10, 6, 22}, {11, 8, 9}}, {{11, 4, 9}, {11, 4, 8}}, {{11, 8, 9}, {11, 4, 10}}} {
		if ValidateDatabaseStep(pair[0], pair[1]) == nil {
			t.Fatal(pair)
		}
	}
	if ValidateDatabaseStep(Version{10, 11, 14}, Version{11, 4, 9}) != nil {
		t.Fatal("reviewed LTS hop rejected")
	}
	if _, err = DatabaseImage("mariadb:lts"); err == nil {
		t.Fatal("floating LTS accepted")
	}
}

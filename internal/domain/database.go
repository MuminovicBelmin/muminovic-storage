package domain

import (
	"fmt"
	"regexp"
	"strings"
)

// Reviewed LTS releases. Floating mariadb:lts is deliberately never pulled.
var databaseReleases = [...]string{"10.6", "10.11", "11.4", "11.8"}
var mariaDBVersion = regexp.MustCompile(`^(\d+\.\d+\.\d+)(?:[-+]|$)`)

func DatabaseVersion(raw string) (Version, error) {
	if !strings.Contains(raw, "MariaDB") {
		return Version{}, fmt.Errorf("the database must identify itself as MariaDB")
	}
	m := mariaDBVersion.FindStringSubmatch(raw)
	if m == nil {
		return Version{}, fmt.Errorf("unrecognized MariaDB version")
	}
	return ParseVersion(m[1])
}
func DatabaseSeries(v Version) string { return fmt.Sprintf("%d.%d", v[0], v[1]) }
func DatabaseImage(tag string) (string, error) {
	for _, series := range databaseReleases[1:] {
		if tag == "mariadb:"+series {
			return series, nil
		}
	}
	return "", fmt.Errorf("only reviewed MariaDB LTS image tags are allowed")
}
func DatabaseCompatible(v Version, nextcloud int) bool {
	s := DatabaseSeries(v)
	return (s == "10.6" && nextcloud <= 34) || s == "10.11" || s == "11.4" || (s == "11.8" && nextcloud >= 33)
}
func DatabasePath(v Version, nextcloud, target int) (before, after []string, err error) {
	if !DatabaseCompatible(v, nextcloud) {
		return nil, nil, fmt.Errorf("unreviewed MariaDB/Nextcloud combination; manual assessment required")
	}
	index := -1
	for i, series := range databaseReleases {
		if series == DatabaseSeries(v) {
			index = i
		}
	}
	if index < 0 {
		return nil, nil, fmt.Errorf("unreviewed MariaDB release; refusing an implicit downgrade")
	}
	// Keep 11.4 while traversing older Nextcloud majors; move to 11.8 only
	// after Nextcloud supports it. Include a same-series patch/image refresh.
	for i := index; i < len(databaseReleases); i++ {
		series := databaseReleases[i]
		if series == "10.6" {
			continue
		}
		if series == "11.8" {
			if target >= 33 {
				after = append(after, series)
			}
		} else {
			before = append(before, series)
		}
	}
	return before, after, nil
}
func ValidateDatabaseStep(current, target Version) error {
	if target.Before(current) {
		return fmt.Errorf("MariaDB downgrades are prohibited")
	}
	a, b := -1, -1
	for i, series := range databaseReleases {
		if series == DatabaseSeries(current) {
			a = i
		}
		if series == DatabaseSeries(target) {
			b = i
		}
	}
	if a < 0 || b < 1 || (b != a && b != a+1) {
		return fmt.Errorf("MariaDB must follow the reviewed LTS sequence")
	}
	return nil
}

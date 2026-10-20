package compose

import (
	"strings"
	"testing"
)

func TestImageEditPreservesOtherSettingsAndFormatting(t *testing.T) {
	for _, source := range []string{
		"services:\n  db:\n    image: mariadb:lts\n  app:\n    image: nextcloud:30 # chosen image\n    restart: unless-stopped\n",
		"services:\r\n    app:\r\n        image: \"nextcloud:30\" # comment\r\n",
	} {
		out, e := ReplaceImage([]byte(source), "nextcloud:31")
		if e != nil {
			t.Fatal(e)
		}
		expected := strings.Replace(source, "\"nextcloud:30\"", "nextcloud:30", 1)
		expected = strings.Replace(expected, "nextcloud:30", "nextcloud:31", 1)
		if string(out) != expected {
			t.Fatalf("unexpected edit:\n%s", out)
		}
	}
}
func TestRejectUnsafeOrAmbiguousImageSource(t *testing.T) {
	for _, source := range []string{"services:\n  app:\n    image: &image nextcloud:30\n", "services:\n  app:\n    image: nextcloud:30\n    image: nextcloud:31\n", "services:\n  app:\n    extends: base\n"} {
		if _, e := ReplaceImage([]byte(source), "nextcloud:31"); e == nil {
			t.Fatalf("accepted %q", source)
		}
	}
}
func TestRejectWrongRepository(t *testing.T) {
	if _, e := ReplaceImage([]byte("services:\n  app:\n    image: nextcloud:30\n"), "other:31"); e == nil {
		t.Fatal("accepted other repository")
	}
}

func TestReplaceEnvironmentPreservesOtherAssignments(t *testing.T) {
	source := []byte("# deployment images\nNEXTCLOUD_IMAGE=nextcloud:33.0.2-apache\nMARIADB_IMAGE=mariadb:lts\n")
	out, err := ReplaceEnvironment(source, "NEXTCLOUD_IMAGE", "nextcloud:34-apache")
	if err != nil {
		t.Fatal(err)
	}
	expected := "# deployment images\nNEXTCLOUD_IMAGE=nextcloud:34-apache\nMARIADB_IMAGE=mariadb:lts\n"
	if string(out) != expected {
		t.Fatalf("unexpected environment edit:\n%s", out)
	}
}

func TestImageChoicesUseReviewedApacheTags(t *testing.T) {
	images, err := ImageChoices(35)
	if err != nil {
		t.Fatal(err)
	}
	for major, image := range map[string]string{"33": "nextcloud:33-apache", "34": "nextcloud:34-apache", "35": "nextcloud:35-apache"} {
		if images[major] != image {
			t.Fatalf("major %s: got %q", major, images[major])
		}
	}
	if _, err = ImageChoices(32); err == nil {
		t.Fatal("accepted an unsupported Nextcloud target")
	}
}

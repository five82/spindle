package media

import "testing"

func TestGetStreamHDRInfoFromMissingFile(t *testing.T) {
	if _, err := GetStreamHDRInfo("/does/not/exist.mkv"); err == nil {
		t.Fatal("expected missing file error")
	}
}

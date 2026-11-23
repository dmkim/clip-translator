// Package update implements self-update from GitHub releases.
package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	selfupdate "github.com/creativeprojects/go-selfupdate/update"

	"github.com/genius/clip-translator/internal/version"
)

const (
	// repository is the GitHub "owner/repo" that publishes releases.
	repository = "dmkim/clip-translator"
	// assetName is the Windows executable attached to each release.
	assetName = "clip-translator.exe"
	// checksumsFile is the release asset containing SHA256 sums.
	checksumsFile = "checksums.txt"
	// maxNotes caps release notes so the confirmation dialog stays readable.
	maxNotes = 1200
	// maxAssetSize limits how much of a download is kept in memory.
	maxAssetSize = 64 << 20
)

// Release describes a newer published version.
type Release struct {
	// Version is the semver of the release, e.g. "0.2.0".
	Version string
	// Notes is the (possibly truncated) release notes.
	Notes string
	// URL is the release page used for manual downloads.
	URL string

	exeURL      string
	checksumURL string
}

// Check looks up the latest GitHub release and returns it when it is newer than
// the running build. It returns (nil, nil) when the build is a development
// build, the current version is not semver, or the app is already up to date.
func Check(ctx context.Context) (*Release, error) {
	if version.IsDev() {
		return nil, nil
	}
	current, err := semver.NewVersion(version.Current())
	if err != nil {
		return nil, nil
	}

	latest, err := fetchLatest(ctx)
	if err != nil {
		return nil, err
	}
	if latest == nil {
		return nil, nil
	}

	tag, err := semver.NewVersion(latest.TagName)
	if err != nil || !tag.GreaterThan(current) {
		return nil, nil
	}

	rel := &Release{
		Version: tag.String(),
		Notes:   truncate(latest.Body, maxNotes),
		URL:     latest.HTMLURL,
	}
	for _, asset := range latest.Assets {
		switch asset.Name {
		case assetName:
			rel.exeURL = asset.URL
		case checksumsFile:
			rel.checksumURL = asset.URL
		}
	}
	if rel.exeURL == "" || rel.checksumURL == "" {
		return nil, fmt.Errorf("릴리스 자산(%s, %s)을 찾을 수 없습니다", assetName, checksumsFile)
	}

	return rel, nil
}

// Apply downloads rel, verifies its SHA256 checksum and replaces the running
// executable. The new binary takes effect after Restart.
func Apply(ctx context.Context, rel *Release) error {
	if rel == nil || rel.exeURL == "" || rel.checksumURL == "" {
		return fmt.Errorf("업데이트 정보가 없습니다")
	}

	expected, err := fetchChecksum(ctx, rel.checksumURL, assetName)
	if err != nil {
		return err
	}

	data, err := download(ctx, rel.exeURL, maxAssetSize)
	if err != nil {
		return fmt.Errorf("업데이트 다운로드 실패: %w", err)
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("실행 파일 경로를 찾을 수 없습니다: %w", err)
	}

	if err := selfupdate.Apply(bytes.NewReader(data), selfupdate.Options{
		TargetPath: exe,
		Checksum:   expected,
	}); err != nil {
		return fmt.Errorf("업데이트 적용 실패: %w", err)
	}
	return nil
}

var beforeRestart func()

func SetBeforeRestart(fn func()) {
	beforeRestart = fn
}

// Restart launches the updated executable and terminates the current process.
func Restart() error {
	if beforeRestart != nil {
		beforeRestart()
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}

	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Dir = filepath.Dir(exe)
	if err := cmd.Start(); err != nil {
		return err
	}

	os.Exit(0)
	return nil
}

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Body    string        `json:"body"`
	HTMLURL string        `json:"html_url"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

func fetchLatest(ctx context.Context) (*githubRelease, error) {
	url := "https://api.github.com/repos/" + repository + "/releases/latest"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	setGitHubHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("최신 버전 확인 실패: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("최신 버전 확인 실패: HTTP %d", resp.StatusCode)
	}

	var rel githubRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("릴리스 정보 해석 실패: %w", err)
	}
	return &rel, nil
}

func fetchChecksum(ctx context.Context, url, name string) ([]byte, error) {
	data, err := download(ctx, url, 256<<10)
	if err != nil {
		return nil, fmt.Errorf("체크섬 다운로드 실패: %w", err)
	}

	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		sum, err := hex.DecodeString(fields[0])
		if err != nil || len(sum) != sha256.Size {
			return nil, fmt.Errorf("체크섬 형식이 올바르지 않습니다")
		}
		return sum, nil
	}
	return nil, fmt.Errorf("체크섬을 찾을 수 없습니다: %s", name)
}

func download(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	setGitHubHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("다운로드 크기가 제한(%d bytes)을 초과했습니다", limit)
	}
	return data, nil
}

func setGitHubHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "clip-translator")
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func truncate(text string, limit int) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

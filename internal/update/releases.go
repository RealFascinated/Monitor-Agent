package update

import (
	"fmt"
	"strings"
	"time"

	"github.com/blang/semver/v4"
)

type release struct {
	TagName     string         `json:"tag_name"`
	Draft       bool           `json:"draft"`
	Prerelease  bool           `json:"prerelease"`
	PublishedAt *time.Time     `json:"published_at"`
	Assets      []releaseAsset `json:"assets"`
}

func latestAgentRelease(releases []release) (release, error) {
	var (
		best    release
		bestVer semver.Version
		found   bool
	)

	for _, item := range releases {
		if !isEligibleAgentRelease(item) {
			continue
		}
		version, err := parseVersion(strings.TrimPrefix(item.TagName, TagPrefix))
		if err != nil {
			continue
		}
		if !found || version.GT(bestVer) || (version.EQ(bestVer) && releaseIsNewer(item, best)) {
			best = item
			bestVer = version
			found = true
		}
	}

	if !found {
		return release{}, fmt.Errorf("no %s releases found", TagPrefix)
	}
	return best, nil
}

func isEligibleAgentRelease(item release) bool {
	if item.Draft || item.Prerelease {
		return false
	}
	return strings.HasPrefix(item.TagName, TagPrefix)
}

func releaseIsNewer(candidate, current release) bool {
	if candidate.PublishedAt == nil {
		return false
	}
	if current.PublishedAt == nil {
		return true
	}
	return candidate.PublishedAt.After(*current.PublishedAt)
}

package ingest

import (
	"net/url"
	"strings"
)

const defaultHeartbeatEndpoint = "https://monitor.fascinated.cc/api/v1/servers/heartbeat"

func (c *Config) HeartbeatURL() string {
	return siblingEndpoint(c.ApiEndpoint, "heartbeat", defaultHeartbeatEndpoint)
}

func siblingEndpoint(apiEndpoint, sibling, fallback string) string {
	trimmed := strings.TrimSpace(apiEndpoint)
	if trimmed == "" {
		return fallback
	}
	if strings.HasSuffix(trimmed, "/ingest") {
		return strings.TrimSuffix(trimmed, "/ingest") + "/" + sibling
	}

	u, err := url.Parse(trimmed)
	if err != nil || u.Path == "" || u.Path == "/" {
		return fallback
	}

	path := strings.TrimSuffix(u.Path, "/")
	if i := strings.LastIndex(path, "/"); i >= 0 {
		u.Path = path[:i+1] + sibling
	} else {
		u.Path = "/" + sibling
	}
	return u.String()
}

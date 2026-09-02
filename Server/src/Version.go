package main

import (
	"fmt"
	"time"
)

var (
	// Injected at build time via -ldflags "-X main.AppVersion=0.26.0902 -X main.AppBuild=1745"
	AppVersion = ""
	AppBuild   = ""
)

// GetAppVersion returns the full display version string, e.g. "ver. 0.26.0902 build 1745"
func GetAppVersion() string {
	ver, build := AppVersion, AppBuild
	if ver == "" || build == "" {
		now := time.Now()
		if ver == "" {
			ver = fmt.Sprintf("0.%s.%s", now.Format("06"), now.Format("0102"))
		}
		if build == "" {
			build = now.Format("1504")
		}
	}
	return fmt.Sprintf("ver. %s build %s", ver, build)
}

// GetVersionTag returns the version tag string, e.g. "0.26.0902"
func GetVersionTag() string {
	if AppVersion != "" {
		return AppVersion
	}
	now := time.Now()
	return fmt.Sprintf("0.%s.%s", now.Format("06"), now.Format("0102"))
}

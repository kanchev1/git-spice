package bitbucketserver

import (
	"context"
	"strconv"

	"golang.org/x/mod/semver"
)

const (
	// _draftMinVersion is the first Data Center version with draft pull requests.
	_draftMinVersion = "8.18.0"

	// _draftMinBuildNumber is the build number for _draftMinVersion (semver fallback).
	_draftMinBuildNumber = 8018000
)

// draftSupport reports whether the server supports draft pull requests.
// known is false when the version cannot be read; version is the raw
// reported version, for diagnostics.
func (r *Repository) draftSupport(ctx context.Context) (supported, known bool, version string) {
	props, err := r.client.ApplicationProperties(ctx)
	if err != nil || props == nil {
		r.log.Debug("Could not read Bitbucket Data Center version; proceeding best-effort", "error", err)
		return false, false, ""
	}

	if semver.IsValid("v" + props.Version) {
		return semver.Compare("v"+props.Version, "v"+_draftMinVersion) >= 0, true, props.Version
	}

	// Fall back to the build number when the version isn't valid semver.
	if n, err := strconv.Atoi(props.BuildNumber); err == nil && n > 0 {
		return n >= _draftMinBuildNumber, true, props.Version
	}

	return false, false, props.Version
}

package bitbucketserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.abhg.dev/gs/internal/forge"
)

// TestPRMetadataRoundTrip ensures a PRMetadata survives a
// Marshal -> Unmarshal cycle with every field intact.
//
// This is the durability backbone: the navigation comment's PRID and
// Version are what let git-spice update or delete the comment after a
// restart, so they must persist verbatim.
func TestPRMetadataRoundTrip(t *testing.T) {
	var f Forge

	orig := &PRMetadata{
		PR: &PR{Number: 42},
		NavigationComment: &PRComment{
			ID:      101,
			PRID:    42,
			Version: 7,
		},
	}

	data, err := f.MarshalChangeMetadata(orig)
	require.NoError(t, err)

	got, err := f.UnmarshalChangeMetadata(data)
	require.NoError(t, err)

	md, ok := got.(*PRMetadata)
	require.True(t, ok, "expected *PRMetadata, got %T", got)

	require.NotNil(t, md.PR)
	assert.Equal(t, int64(42), md.PR.Number)

	require.NotNil(t, md.NavigationComment)
	assert.Equal(t, int64(101), md.NavigationComment.ID)
	assert.Equal(t, int64(42), md.NavigationComment.PRID)
	assert.Equal(t, 7, md.NavigationComment.Version)

	// The fully reconstructed value must equal the original.
	assert.Equal(t, orig, md)
}

// TestPRMetadataRoundTrip_noComment ensures a PRMetadata with no
// navigation comment round-trips with a nil comment.
func TestPRMetadataRoundTrip_noComment(t *testing.T) {
	var f Forge

	orig := &PRMetadata{PR: &PR{Number: 7}}

	data, err := f.MarshalChangeMetadata(orig)
	require.NoError(t, err)

	got, err := f.UnmarshalChangeMetadata(data)
	require.NoError(t, err)

	md, ok := got.(*PRMetadata)
	require.True(t, ok, "expected *PRMetadata, got %T", got)

	require.NotNil(t, md.PR)
	assert.Equal(t, int64(7), md.PR.Number)
	assert.Nil(t, md.NavigationComment)
	assert.Equal(t, orig, md)
}

// TestChangeIDRoundTrip ensures a PR change ID survives a
// MarshalChangeID -> UnmarshalChangeID cycle.
func TestChangeIDRoundTrip(t *testing.T) {
	var f Forge

	orig := &PR{Number: 123}

	data, err := f.MarshalChangeID(orig)
	require.NoError(t, err)

	got, err := f.UnmarshalChangeID(data)
	require.NoError(t, err)

	pr, ok := got.(*PR)
	require.True(t, ok, "expected *PR, got %T", got)
	assert.Equal(t, int64(123), pr.Number)
	assert.Equal(t, orig, pr)
}

// TestUnmarshalChangeMetadata_invalid ensures malformed metadata yields a
// wrapped error rather than a panic.
func TestUnmarshalChangeMetadata_invalid(t *testing.T) {
	var f Forge

	_, err := f.UnmarshalChangeMetadata([]byte("not json"))
	require.Error(t, err)
	assert.ErrorContains(t, err, "unmarshal PR metadata")
}

// TestUnmarshalChangeID_invalid ensures a malformed change ID yields a
// wrapped error rather than a panic.
func TestUnmarshalChangeID_invalid(t *testing.T) {
	var f Forge

	_, err := f.UnmarshalChangeID([]byte("not json"))
	require.Error(t, err)
	assert.ErrorContains(t, err, "unmarshal PR ID")
}

var (
	_ forge.ChangeMetadata = (*PRMetadata)(nil)
	_ forge.ChangeID       = (*PR)(nil)
)

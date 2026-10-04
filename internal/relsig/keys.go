package relsig

// ReleaseKeys are the public keys allowed to sign checksums.txt. Add the new
// key before rotating, and keep the old one for as long as releases signed
// with it should still verify. TestReleaseKeysConfigured fails while the list
// is empty, so a release cannot ship without one.
var ReleaseKeys = []string{
	"5r/VTDFnWuvqWN2aMxp3Gn3KZOxbDvdkwgw/8gwV6Co=", // 2026-10-04, first release key
	"T7oC0UPkoBuTW54rFaVG8fgvzFvsp2m1VYZ24HDUts0=", // 2026-10-04, offline backup key (not in CI)
}

package oauth

// ExportComputePKCEChallengeS256 exposes computePKCEChallengeS256
// to the external test package without making the function public.
// Slice-189 test seam.
func ExportComputePKCEChallengeS256(verifier string) string {
	return computePKCEChallengeS256(verifier)
}

// ExportConstantTimeEqual exposes constantTimeEqualString to the
// external test package.
func ExportConstantTimeEqual(a, b string) bool {
	return constantTimeEqualString(a, b)
}

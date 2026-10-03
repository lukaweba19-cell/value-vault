// Package keys holds the Cloudflare R2 credentials and object keys used for
// update distribution and cloud quota storage.
package keys

const (
	EndpointHost   = "30eb01bfd5ca05f0b5bf5e9acfe67bab.r2.cloudflarestorage.com"
	AccessKey      = "705190a241bba3f397b00f7eb1efceaf"
	SecretKey      = "9b609b3ff870a9fb158e7975eacea2970f9fa1273601ce0d577c5b87a6900202"
	WriteAccessKey = "66822e3237a884097f4a57a568cc37a8"
	WriteSecretKey = "eae80ff80f184f30339f2e372b2866e79b15eb51382dfcae8978f181165f81e3"
	Bucket         = "solo-checker"
	ManifestKey    = "manifest.json"
	// ExeKey is the launcher/installer beside the window. Legacy names the
	// boot path still relocates from: xbox-checker.exe (≤1.0.16) and
	// helper.exe (1.0.17–1.0.18).
	ExeKey      = "installer.exe"
	DllKey      = "solo.dll"
	BundleKey   = "solo-checker-bundle.zip"
	UsersPrefix = "users/"
)

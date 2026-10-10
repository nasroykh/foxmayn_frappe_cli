package services

// errorKeys is the English text of every Error.Key, with {{name}}
// placeholders for Error.Args. The page's en.json holds the same text under
// errors.<key> (TestErrorKeysMatchCatalog), and fr.json and ar.json their
// translations.
var errorKeys = map[string]string{
	"site.refused":     "The site did not accept these sign-in details.",
	"site.unreachable": "The site could not be reached. Check the address and your internet connection.",
	"site.status":      "The site answered with an error ({{status}}).",

	"site.engineCancelled":     "Starting the assistant engine was cancelled.",
	"site.engineFailed":        "Starting the assistant engine failed.",
	"site.oauthSetupCancelled": "Setting up the sign-in was cancelled.",
	"site.oauthSetupFailed":    "Setting up the sign-in failed.",
	"site.signInCancelled":     "Signing in was cancelled.",
	"site.signInFailed":        "Signing in failed.",
	"site.apiKeyCancelled":     "Checking the API key was cancelled.",
	"site.apiKeyFailed":        "Checking the API key failed.",
	"site.checkCancelled":      "Checking the connection was cancelled.",
	"site.checkFailed":         "Checking the connection failed.",
	"site.renewCancelled":      "Renewing the sign-in was cancelled.",
	"site.renewFailed":         "Renewing the sign-in failed.",
	"site.newURLCancelled":     "Checking the new address was cancelled.",
	"site.newURLFailed":        "Checking the new address failed.",

	"site.connected":      "Connected",
	"site.signInExpired":  "Your sign-in has expired. Sign in again to keep using this site.",
	"site.signInRejected": "The site no longer accepts this sign-in. Sign in again.",
}

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

	"chat.busy":           "The assistant is still answering in this conversation.",
	"chat.busyStopFirst":  "The assistant is still answering in this conversation. Stop it first.",
	"chat.notPaused":      "This run is not paused.",
	"chat.shuttingDown":   "The assistant is shutting down.",
	"chat.gone":           "That conversation or run no longer exists.",
	"chat.store":          "Could not read or save the conversation.",
	"chat.save":           "Could not save the conversation.",
	"chat.keyRefused":     "The provider did not accept the API key.",
	"chat.modelStopped":   "The model stopped without an answer: {{reason}}.",
	"chat.providerError":  "The AI provider returned an error.",
	"chat.failed":         "The assistant stopped because of an error.",
	"chat.approvalGone":   "This request was already answered or has ended.",
	"chat.messageGone":    "That message is no longer in the conversation.",
	"chat.notAPrompt":     "Only your own messages can be edited or deleted.",
	"chat.importedPrompt": "A message from an imported file cannot be edited or run again.",
	"chat.nothingToRetry": "There is no message to run again.",
	"chat.tooManyToEdit":  "Remove some files from the message box first: with this message's files there would be more than {{max}}.",

	"apps.unknown":            "Unknown app.",
	"apps.ffcMissing":         "Install ffc first: apps reach your sites through it.",
	"apps.configUnreadable":   "The ffc config file could not be read.",
	"apps.noSite":             "There is no site called “{{site}}” any more.",
	"apps.noUserFolders":      "Your user folders could not be found.",
	"apps.settingsUnreadable": "The app's settings file could not be read, so it was left as it is.",
	"apps.settingsUnchanged":  "The app's settings could not be changed.",
	"apps.claudeNotFound":     "The claude command was not found. Install Claude Code first, or run the commands below yourself.",
	"apps.claudeBatch":        "The claude command is a Windows script the app does not run. Run the commands below yourself.",
	"apps.readOnly":           "The app's settings file is read-only.",
}

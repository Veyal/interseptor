package control

import "testing"

func TestUIActivityLoadErrorSurvivesTabActivation(t *testing.T) {
	activity := executableJS(readUIAsset(t, "js/activity.js"))
	requireUIContains(t, activity,
		"let activityLoadError=false",
		"if(activityLoadError)return;",
		"activityLoadError=true",
		"renderLoadError",
		"clearActivityLoadError();renderActivity();",
	)
}

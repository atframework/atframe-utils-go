package buildsetting

type BuildMananger interface {
	Init() error
	SetDocDir(dir string) error

	SetTool(toolName string, version string, path string) error
	GetToolPath(toolName string) (string, error)
	ResetTool(toolName string) error

	ListTools() (map[string]string, error)
}

var NewBuildManager = NewManagerInDir
var BuildManagerLoad = NewManagerLoadExistSettingsFile

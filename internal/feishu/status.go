package feishu

type BinaryStatus struct {
	Found   bool   `json:"found"`
	Version string `json:"version,omitempty"`
	Path    string `json:"path,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

type SkillStatus struct {
	Name    string `json:"name"`
	Found   bool   `json:"found"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message,omitempty"`
}

type ConfigStatus struct {
	Configured bool   `json:"configured"`
	AppID      string `json:"appId,omitempty"`
	Brand      string `json:"brand,omitempty"`
	Path       string `json:"path,omitempty"`
	Message    string `json:"message,omitempty"`
}

type AuthStatus struct {
	Authorized       bool   `json:"authorized"`
	Verified         bool   `json:"verified"`
	Identity         string `json:"identity,omitempty"`
	UserName         string `json:"userName,omitempty"`
	UserOpenID       string `json:"userOpenId,omitempty"`
	ExpiresAt        string `json:"expiresAt,omitempty"`
	RefreshExpiresAt string `json:"refreshExpiresAt,omitempty"`
	TokenStatus      string `json:"tokenStatus,omitempty"`
	Message          string `json:"message,omitempty"`
}

type Status struct {
	Platform       string        `json:"platform"`
	Arch           string        `json:"arch"`
	Node           BinaryStatus  `json:"node"`
	NPM            BinaryStatus  `json:"npm"`
	NPX            BinaryStatus  `json:"npx"`
	CLI            BinaryStatus  `json:"cli"`
	Skills         []SkillStatus `json:"skills"`
	SkillsReady    bool          `json:"skillsReady"`
	Config         ConfigStatus  `json:"config"`
	Auth           AuthStatus    `json:"auth"`
	Running        bool          `json:"running"`
	SetupRunning   bool          `json:"setupRunning"`
	LoginRunning   bool          `json:"loginRunning"`
	InstallRunning bool          `json:"installRunning"`
	SetupURL       string        `json:"setupUrl,omitempty"`
	LoginURL       string        `json:"loginUrl,omitempty"`
	LastOutput     string        `json:"lastOutput,omitempty"`
	LastError      string        `json:"lastError,omitempty"`
	LastStartedAt  string        `json:"lastStartedAt,omitempty"`
	CurrentModel   string        `json:"currentModel,omitempty"`
	RequiredSkills []string      `json:"requiredSkills,omitempty"`
}

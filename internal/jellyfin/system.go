package jellyfin

import "net/http"

type publicSystemInfo struct {
	LocalAddress           string `json:"LocalAddress"`
	ServerName             string `json:"ServerName"`
	Version                string `json:"Version"`
	ProductName            string `json:"ProductName"`
	OperatingSystem        string `json:"OperatingSystem"`
	Id                     string `json:"Id"`
	StartupWizardCompleted bool   `json:"StartupWizardCompleted"`
}

func (s *Server) public(r *http.Request) publicSystemInfo {
	return publicSystemInfo{LocalAddress: s.Network.Origin(r), ServerName: "Antresol", Version: s.Version,
		ProductName: "Jellyfin Server", OperatingSystem: "Linux", Id: s.sid(), StartupWizardCompleted: true}
}

func (s *Server) publicInfo(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.public(r)) }

func (s *Server) systemInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, struct {
		publicSystemInfo
		HasPendingRestart      bool `json:"HasPendingRestart"`
		IsShuttingDown         bool `json:"IsShuttingDown"`
		CanSelfRestart         bool `json:"CanSelfRestart"`
		SupportsLibraryMonitor bool `json:"SupportsLibraryMonitor"`
	}{publicSystemInfo: s.public(r)})
}

func (s *Server) ping(w http.ResponseWriter, r *http.Request) { writeJSON(w, "Jellyfin Server") }

func (s *Server) branding(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, struct {
		LoginDisclaimer     string `json:"LoginDisclaimer"`
		CustomCss           string `json:"CustomCss"`
		SplashscreenEnabled bool   `json:"SplashscreenEnabled"`
	}{})
}

func (s *Server) brandingCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.WriteHeader(http.StatusOK)
}

func (s *Server) quickConnect(w http.ResponseWriter, r *http.Request) { writeJSON(w, false) }

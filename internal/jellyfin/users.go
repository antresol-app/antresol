package jellyfin

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
)

// userDto: every profile "has a password" here, the app password is
// always needed.
func (s *Server) userDto(u auth.User) UserDto {
	return UserDto{
		Name: u.Name, Id: UserID(u.ID), ServerId: s.sid(), HasPassword: true, HasConfiguredPassword: true,
		Configuration: &UserConfiguration{
			PlayDefaultAudioTrack: true, SubtitleMode: "Default",
			OrderedViews: []string{}, MyMediaExcludes: []string{}, LatestItemsExcludes: []string{},
			RememberAudioSelections: true, RememberSubtitleSelections: true, EnableNextEpisodeAutoPlay: true,
		},
		Policy: &UserPolicy{
			EnableMediaPlayback: true, EnableAudioPlaybackTranscoding: true, EnableVideoPlaybackTranscoding: true,
			EnablePlaybackRemuxing: true, EnableContentDownloading: true, EnableAllFolders: true, EnableRemoteAccess: true,
			AuthenticationProviderId: authProvider, PasswordResetProviderId: resetProvider,
		},
	}
}

// sessionDto describes the token's session. Its Id is derived from the
// token by a one-way hash, so it never reveals the token.
func (s *Server) sessionDto(u auth.User, se session) SessionInfoDto {
	h := sha256.Sum256([]byte("jellyfin-session:" + se.token))
	return SessionInfoDto{
		Id: hex.EncodeToString(h[:16]), UserId: UserID(u.ID), UserName: u.Name,
		Client: se.dev.Client, DeviceId: se.dev.ID, DeviceName: se.dev.Name, ApplicationVersion: se.dev.Version,
		ServerId: s.sid(), IsActive: true, LastActivityDate: jfTime(time.Now()),
		PlayableMediaTypes: []string{}, SupportedCommands: []string{},
	}
}

// publicUsers is the profile picker: at home every profile, from outside
// none (the names are not for strangers).
func (s *Server) publicUsers(w http.ResponseWriter, r *http.Request) {
	out := []UserDto{}
	if s.Network.IsTrusted(r) {
		list, err := s.Users.List(r.Context())
		if err != nil {
			s.internal(w, r, "users", err)
			return
		}
		for _, u := range list {
			out = append(out, UserDto{Name: u.Name, Id: UserID(u.ID), ServerId: s.sid(), HasPassword: true, HasConfiguredPassword: true})
		}
	}
	writeJSON(w, out)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.userDto(userOf(r))) }

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.Tokens.Revoke(r.Context(), sessionOf(r).token); err != nil {
		s.internal(w, r, "logout", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sessions lists the caller's own session only (other devices and
// profiles are not its business).
func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	se := sessionOf(r)
	out := []SessionInfoDto{}
	if d := qv(r, "deviceId"); d == "" || d == se.dev.ID {
		out = append(out, s.sessionDto(userOf(r), se))
	}
	writeJSON(w, out)
}

// displayPreferences: Vault keeps none; the defaults, with the id echoed
// as the client sent it.
func (s *Server) displayPreferences(w http.ResponseWriter, r *http.Request) {
	id := ""
	if p, ok := r.Context().Value(origPathKey{}).(string); ok {
		parts := strings.Split(strings.Trim(p, "/"), "/")
		if len(parts) >= 2 {
			id = parts[1]
		}
	}
	writeJSON(w, struct {
		Id          string            `json:"Id"`
		CustomPrefs map[string]string `json:"CustomPrefs"`
		SortBy      string            `json:"SortBy"`
		SortOrder   string            `json:"SortOrder"`
	}{id, map[string]string{}, "SortName", "Ascending"})
}

package session

import (
	"net/http"
	"time"
)

type CookieConfig struct {
	Name     string
	Secure   bool
	SameSite http.SameSite
	Domain   string
}

func ParseSameSite(s string) http.SameSite {
	if s == "Lax" {
		return http.SameSiteLaxMode
	}

	if s == "Strict" {
		return http.SameSiteStrictMode
	}

	return http.SameSiteLaxMode
}

func SetCookie(w http.ResponseWriter, cfg CookieConfig, rawToken string, expires time.Time) {
	cookie := http.Cookie{
		Name:     cfg.Name,
		Path:     "/",
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: cfg.SameSite,
		Domain:   cfg.Domain,
		Expires:  expires,
		Value: rawToken,
	}
	http.SetCookie(w, &cookie)
}

func ClearCookie(w http.ResponseWriter, cfg CookieConfig) {
	cookie := http.Cookie{
		Name:     cfg.Name,
		Path:     "/",
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: cfg.SameSite,
		Domain:   cfg.Domain,
		Value:    "",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	}
	http.SetCookie(w, &cookie)
}

func ReadCookie(r *http.Request, cfg CookieConfig) (string, bool) {
	c, err := r.Cookie(cfg.Name)
	if err != nil {
		return "", false
	}
	return c.Value, true
}

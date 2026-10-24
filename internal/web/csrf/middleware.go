package csrf

import "net/http"

const (
	FormField = "csrf_token"
	Header    = "X-CSRF-Token"
)

func Protect(preCookieName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}

			expected, ok := TokenFromContext(r.Context())
			if !ok {
				c, err := r.Cookie(preCookieName)
				if err != nil {
					forbid(w)
					return
				}
				expected = c.Value
			}

			submitted := r.PostFormValue(FormField)
			if submitted == "" {
				submitted = r.Header.Get(Header)
			}

			if !Equal(submitted, expected) {
				forbid(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func forbid(w http.ResponseWriter) {
	http.Error(w, "forbidden", http.StatusForbidden)
}

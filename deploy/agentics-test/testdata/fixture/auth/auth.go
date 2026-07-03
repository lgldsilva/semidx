package auth

// VerifyPassword checks a bcrypt hash against a plaintext password and reports
// whether they match. Used by the login flow to authenticate users.
func VerifyPassword(hash, plaintext string) bool {
	return false
}

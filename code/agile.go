package code

/*
  400100000 -> 400199999: agile service codes
*/

type IdentityCode Code

const (
	InvalidToken         IdentityCode = 400100000
	UnknownIdentityError              = 400199999
)

func (i IdentityCode) String() string {
	switch i {
	case InvalidToken:
		return "invalid token"
	case UnknownIdentityError:
		return "unknown identity error"
	default:
		return "unknown identity error"
	}
}

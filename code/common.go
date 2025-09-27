package code

type Code uint32

/*
error code description: 4(placeholder) 000-999(service) 00000-99999(code)
400000000 -> 400099999: common codes
*/

const (
	InvalidArgument Code = 400000000
	NotFoundError   Code = 400000001
	UnknownError    Code = 400099999
)

func (c Code) String() string {
	switch c {
	case InvalidArgument:
		return "invalid argument"
	case NotFoundError:
		return "not found"
	case UnknownError:
		return "unknown error"
	default:
		return "unknown error"
	}
}

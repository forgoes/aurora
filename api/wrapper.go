package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"github.com/forgoes/aurora/code"
	"github.com/forgoes/aurora/runtime"
)

type Payload struct {
	Code    code.Code     `json:"code"`
	Message string        `json:"message"`
	Details []interface{} `json:"details"`
}

type Error struct {
	Status  int
	Payload *Payload
}

type Handler func(c *Context) (interface{}, *Error)

type DataType int

const (
	DataTypeJson     DataType = iota // exactly JSON and already JSON header set
	DataTypeJsonStr                  // exactly JSON but with no JSON header set
	DataTypePlainStr                 // plain text
)

type WrapConfig struct {
	RespDataType DataType
}

func WithDataType(t DataType) func(config *WrapConfig) {
	return func(w *WrapConfig) {
		w.RespDataType = t
	}
}

type Context struct {
	Runtime   *runtime.Runtime
	GinCtx    *gin.Context
	IsLogin   bool
	UserID    uint
	UserName  string
	UserEmail string
	UserRole  string
	MetaMasks []string
}

const tokenPrefix = "token "

func parseToken(token string, key []byte) (*JWTClaims, error) {
	tokenClaims, err := jwt.ParseWithClaims(token, &JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		return key, nil
	})
	if err != nil {
		return nil, err
	}
	if tokenClaims == nil {
		return nil, errors.New("invalid claims")
	}

	claims, ok := tokenClaims.Claims.(*JWTClaims)
	if !ok {
		return nil, errors.New("invalid claims")
	}

	if !tokenClaims.Valid {
		return nil, errors.New("invalid claims")
	}

	return claims, nil
}

// Wrap
// success:               status - 200
// bad request:           status - 400
// unauthorized:          status - 401
// forbidden:             status - 403
// internal server error: status - 500
func Wrap(h Handler, rt *runtime.Runtime, loginRequired bool, opts ...func(*WrapConfig)) gin.HandlerFunc {
	w := &WrapConfig{
		RespDataType: DataTypeJson,
	}

	for _, opt := range opts {
		opt(w)
	}

	return func(gCtx *gin.Context) {
		c := Context{
			Runtime:   rt,
			GinCtx:    gCtx,
			IsLogin:   false,
			UserID:    0,
			UserName:  "",
			UserEmail: "",
			UserRole:  "",
			MetaMasks: make([]string, 0),
		}

		if loginRequired {
			authToken := gCtx.GetHeader("Authorization")
			if len(authToken) <= len(tokenPrefix) {
				gCtx.AbortWithStatus(401)
				return
			}
			token := authToken[len(tokenPrefix):]
			claims, err := parseToken(token, []byte(rt.Config.Jwt.Key))
			if err != nil {
				gCtx.AbortWithStatus(401)
				return
			}
			c.IsLogin = true
			c.UserID = claims.UserId
			c.UserName = claims.UserName
			c.UserEmail = claims.UserEmail
			c.UserRole = claims.UserRole
			c.MetaMasks = claims.MetaMasks
		}

		data, err := h(&c)

		if err != nil {
			if err.Status == http.StatusInternalServerError {
				gCtx.String(http.StatusInternalServerError, err.Payload.Message)
			} else if err.Status == http.StatusSeeOther {
				gCtx.Redirect(http.StatusSeeOther, data.(string))
			} else {
				gCtx.JSON(err.Status, err.Payload)
			}
		} else {
			switch w.RespDataType {
			case DataTypeJson:
				gCtx.JSON(http.StatusOK, data)
			case DataTypeJsonStr:
				gCtx.Header("Content-Type", "application/json; charset=utf-8")
				gCtx.String(http.StatusOK, data.(string))
			case DataTypePlainStr:
				gCtx.String(http.StatusOK, data.(string))
			default:
				gCtx.JSON(http.StatusOK, data)
			}
		}
	}
}

func InvalidArgument(details []interface{}, messages ...string) *Error {
	if len(messages) > 0 {
		return &Error{
			Status: http.StatusBadRequest,
			Payload: &Payload{
				Code:    code.InvalidArgument,
				Message: messages[0],
				Details: details,
			},
		}
	}

	return &Error{
		Status: http.StatusBadRequest,
		Payload: &Payload{
			Code:    code.InvalidArgument,
			Message: code.InvalidArgument.String(),
			Details: details,
		},
	}
}

func InternalServerError(messages ...string) *Error {
	if len(messages) > 0 {
		return &Error{
			Status: http.StatusInternalServerError,
			Payload: &Payload{
				Message: messages[0],
			},
		}
	}

	return &Error{
		Status: http.StatusInternalServerError,
		Payload: &Payload{
			Message: "500 Internal Server Error",
		},
	}
}

func StatusUnauthorizedError(messages ...string) *Error {
	if len(messages) > 0 {
		return &Error{
			Status: http.StatusUnauthorized,
			Payload: &Payload{
				Message: messages[0],
			},
		}
	}

	return &Error{
		Status: http.StatusUnauthorized,
		Payload: &Payload{
			Message: "unauthorized",
		},
	}
}

func NotFoundError(messages ...string) *Error {
	if len(messages) > 0 {
		return &Error{
			Status: http.StatusBadRequest,
			Payload: &Payload{
				Code:    code.NotFoundError,
				Message: messages[0],
			},
		}
	}

	return &Error{
		Status: http.StatusBadRequest,
		Payload: &Payload{
			Code:    code.NotFoundError,
			Message: code.NotFoundError.String(),
		},
	}
}

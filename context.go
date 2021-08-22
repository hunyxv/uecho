package uecho

import (
	"context"
	"net/url"

	"github.com/labstack/echo/v4"
	"github.com/sirupsen/logrus"
)

// HttpApiResponse 响应
type HttpApiResponse struct {
	EC   int         `json:"ec"`
	EM   string      `json:"em"`
	Data interface{} `json:"data,omitempty"`
}

var _ echo.Context = (*Context)(nil)

// Context 	 自定义上下文，组合自 echo.Context
// 可进行自定义扩展
type Context struct {
	echo.Context
	logger *logrus.Logger
}

func (c *Context) init(ec echo.Context) {
	c.Context = ec
}

func (c *Context) reset() {
	c.Context = nil
	c.logger = nil
}

// RequestContext Request 的 ctx
func (c *Context) RequestContext() context.Context {
	return c.Request().Context()
}

// Method 请求的method
func (c *Context) Method() string {
	return c.Request().Method
}

// Host 请求的host
func (c *Context) Host() string {
	return c.Request().Host
}

// URI unescape 后的 uri
func (c *Context) URI() string {
	uri, _ := url.QueryUnescape(c.Request().URL.RequestURI())
	return uri
}

func (c *Context) GetHeader(key string) string {
	return c.Request().Header.Get(key)
}

func (c *Context) SetHeader(key, value string) {
	c.Request().Header.Set(key, value)
}

func (c *Context) SetRespHeader(key, value string) {
	c.Response().Header().Set(key, value)
}

func (c *Context) Logrus() *logrus.Logger {
	if c.logger != nil {
		return c.logger
	}
	return logrus.StandardLogger()
}

func (c *Context) setLogrus(logger *logrus.Logger) {
	c.logger = logger
}

// SetPayload 写入响应,http 状态码大于 400 就当作异常处理
func (c *Context) SetPayload(payload Reply) error {
	p := payload.(*reply)
	if p.httpCode >= 400 {
		return &errReply{Reply: p}
	}

	return c.JSON(p.httpCode, &HttpApiResponse{
		EC:   p.ec,
		EM:   p.em,
		Data: p.data,
	})
}

// Abort 终止处理，返回携带状态码的异常
func (c *Context) Abort(reply Reply) ErrReply {
	er := errReplyPool.Get().(*errReply)
	er.reset()
	er.Reply = reply
	return er
}

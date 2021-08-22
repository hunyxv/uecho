package uecho

import (
	"context"
	"errors"
	"log"
	"testing"
	"time"

	"github.com/hunyxv/utils/shutdown"
	"github.com/labstack/echo/v4"
	"github.com/sirupsen/logrus"
)

// ATestHandler Handler
type ATestHandler struct {
	Field1 string `json:"field1"`
}

func (h *ATestHandler) Handle(ctx *Context) error {
	ctx.SetRespHeader("test-key", "abcdefg")
	field := ctx.QueryParam("field1")
	h.Field1 = field
	return ctx.JSON(200, h)
}

func startHttpServer() *UEcho {
	ue := New(nil)
	ue.Debug = true
	logrus.SetReportCaller(true)
	premw := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			log.Println("这是 premw")
			return next(c)
		}
	}
	ue.Pre(premw, Logger()) // 添加全局中间键 premw 和 logger（）

	mw := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()
			err := next(c)
			log.Println("这是 mw, 用时:", time.Since(start).String())
			if err != nil {
				log.Printf("%+v", err)
			}
			return err
		}
	}
	v1 := ue.Group("/v1", mw)
	v2 := ue.Group("/v2", mw)

	v1.GET("/hello", HandlerFunc(func(c *Context) error {
		err := errors.New("内部错误")
		// 发送错误不用单独打印日志 （log.Error()...等）
		// 直接 return c.Abort(Reply).WithErr(error)
		return c.Abort(ErrInternal).WithErr(err).WithField("rpc", "/service/session-service") // 500 错误，会打印 error 日志
	}))

	v2.POST("/hello", HandlerFunc(func(c *Context) error {
		resp := struct {
			Field1 string `json:"field_1"`
			Field2 int    `json:"field_2"`
		}{Field1: "field_1", Field2: 100000000}
		return c.SetPayload(OK.WithData(resp)) // // 会打印 info 日志
	}))

	ue.GET("/v3/hello", HandlerFunc(func(c *Context) error {
		return c.Abort(ErrIllegalparams).WithErr(errors.New("参数解析失败")) // 会打印 warn 日志
	}))

	h := &ATestHandler{Field1: "Handler"}
	ue.GET("/v4/handler", h, mw)

	return ue
}

func TestUEcho(t *testing.T) {
	ue := startHttpServer()
	go func() {
		if err := ue.Start(":12345"); err != nil {
			t.Fatal(err)
		}
	}()
	hook := shutdown.NewHook()
	hook.Add(func() {
		ctx := context.Background()
		err := ue.Shutdown(ctx)
		if err != nil {
			t.Fatal(err)
		}
	})
	hook.WatchSignal()
}

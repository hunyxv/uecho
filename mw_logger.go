package uecho

import (
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/sirupsen/logrus"
)

type LoggerConfig struct {
	// Skipper defines a function to skip middleware.
	Skipper middleware.Skipper
}

func Logger() echo.MiddlewareFunc {
	return LoggerWithConfig(LoggerConfig{})
}

// LoggerWithConfig 日志中间键
func LoggerWithConfig(conf LoggerConfig) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		f := func(c *Context) (err error) {
			if conf.Skipper != nil && conf.Skipper(c) {
				return next(c)
			}

			req := c.Request()
			res := c.Response()
			start := time.Now()
			if err = next(c); err != nil {
				c.Error(err)
			}
			stop := time.Now()

			entry := c.Logrus().WithFields(logrus.Fields{
				"host":       req.Host,
				"uri":        req.RequestURI,
				"method":     req.Method,
				"protocol":   req.Proto,
				"user_agent": req.UserAgent(),
				"status":     res.Status,
				"latency":    stop.Sub(start).String(),
			})

			if err != nil { 
				// 状态码 >= 500 即发生异常
				if res.Status >= 500 {
					if errreply, ok := err.(*errReply); ok {
						entry.WithFields(errreply.fields).WithError(err).Error()
						return
					}
					entry.WithError(err).Error()
					return
				}
				// 状态码 < 400 打印 warn 级别日志
				if errreply, ok := err.(*errReply); ok {
					entry.WithFields(errreply.fields).WithError(err).Warn()
					return
				}
				entry.WithError(err).Warn()
				return
			}
			// info
			entry.Info()
			return
		}

		return WrapHandler(HandlerFunc(f))
	}
}

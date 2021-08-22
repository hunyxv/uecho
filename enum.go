package uecho

import (
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/pkg/errors"
	"go.uber.org/multierr"
)

var (
	LANG_ZH_CN   = "zh-CN"
	LANG_ZH_TW   = "zh-TW"
	LANG_EN_US   = "en-US"
	LANG_DEFAULT = LANG_ZH_CN

	eci18n = make(map[string]string)
)

func init() {
	eci18n["200."+LANG_ZH_CN] = "请求成功"
	eci18n["200."+LANG_ZH_TW] = "请求成功"
	eci18n["200."+LANG_EN_US] = "Success"

	eci18n["400."+LANG_ZH_CN] = "请求失败"
	eci18n["400."+LANG_ZH_TW] = "请求失败"
	eci18n["400."+LANG_EN_US] = "Fail"

	eci18n["401."+LANG_ZH_CN] = "非法的请求API协议"
	eci18n["401."+LANG_ZH_TW] = "非法的请求API协议"
	eci18n["401."+LANG_EN_US] = "Invalid Protocol"

	eci18n["403."+LANG_ZH_CN] = "权限验证失败，请重新登录"
	eci18n["403."+LANG_ZH_TW] = "权限验证失败，请重新登录"
	eci18n["403."+LANG_EN_US] = "Permission Denied,Please Login! "

	eci18n["404."+LANG_ZH_CN] = "流量控制"
	eci18n["404."+LANG_ZH_TW] = "流量控制"
	eci18n["404."+LANG_EN_US] = "Flow Controlled"

	eci18n["405."+LANG_ZH_CN] = "暂不支持的服务"
	eci18n["405."+LANG_ZH_TW] = "暂不支持的服务"
	eci18n["405."+LANG_EN_US] = "Service Not Found"

	eci18n["410."+LANG_ZH_CN] = "状态已经失效"
	eci18n["410."+LANG_ZH_TW] = "状态已经失效"
	eci18n["410."+LANG_EN_US] = "Session has been expired !"

	eci18n["500."+LANG_ZH_CN] = "服务器内部错误,请稍后再试"
	eci18n["500."+LANG_ZH_TW] = "服务器内部错误,请稍后再试"
	eci18n["500."+LANG_EN_US] = "Server Internal Error !"

	eci18n["501."+LANG_ZH_CN] = "参数错误"
	eci18n["501."+LANG_ZH_TW] = "参数错误"
	eci18n["501."+LANG_EN_US] = "Parameters are invalid  !"

	eci18n["502."+LANG_ZH_CN] = "读取微信服务器数据失败，请稍后再试"
	eci18n["502."+LANG_ZH_TW] = "参数错误"
	eci18n["502."+LANG_EN_US] = "Fecthing informations from wechat's endpoint has been fail, Please try later!"

	eci18n["10302."+LANG_ZH_CN] = "加密方式已变更!"
	eci18n["10302."+LANG_ZH_TW] = "Encrypt Method has been changed!"
	eci18n["10302."+LANG_EN_US] = "Encrypt Method has been changed!"
}

var errReplyPool = sync.Pool{
	New: func() interface{} {
		return &errReply{}
	},
}

var _ Reply = (*reply)(nil)

// Reply 响应
type Reply interface {
	WithHTTPCode(int) Reply     // 设置 http 状态码
	HTTPCode() int              // 返回 http 状态码
	WithEC(int) Reply           // 设置业务码
	EC() int                    // 返回业务码
	WithEM(string) Reply        // 描述信息
	EM() string                 // 返回描述信息
	I18n(string) string         // 对应I18n描述
	WithLang(string) Reply      // 设置 lang
	WithData(interface{}) Reply // 写入响应数据
	reply()
}

var _ ErrReply = (*errReply)(nil)

// ErrReply 异常响应
type ErrReply interface {
	WithField(string, interface{}) ErrReply     // 向响应中添加其他信息
	WithFields(map[string]interface{}) ErrReply // 向响应中添加其他信息
	WithErr(error) ErrReply                     // 向响应中添加 error
	Error() string                              // errors interface
	reset()
}

func NewReply(httpCode, ec int, em string) Reply {
	return &reply{
		httpCode: httpCode,
		ec:       ec,
		em:       em,
		lang:     LANG_DEFAULT,
	}
}

func NewErrReply(httpCode, ec int, em string, err error) ErrReply {
	return &errReply{
		Reply: NewReply(httpCode, ec, em),
		err:   err,
	}
}

type reply struct {
	httpCode int
	ec       int
	em       string
	lang     string
	data     interface{}
}

func (r *reply) WithHTTPCode(c int) Reply {
	clone := *r
	clone.httpCode = c
	return &clone
}

func (r *reply) HTTPCode() int {
	return r.httpCode
}

func (r *reply) WithEC(ec int) Reply {
	clone := *r
	clone.ec = ec
	return &clone
}

func (r *reply) EC() int {
	return r.ec
}

func (r *reply) WithEM(em string) Reply {
	clone := *r
	clone.em = em
	return &clone
}

func (r *reply) EM() string {
	return r.em
}

func (r *reply) WithLang(lang string) Reply {
	clone := *r
	clone.lang = lang
	return &clone
}

func (r *reply) I18n(lang string) string {
	r.lang = lang
	if em, ok := eci18n[fmt.Sprintf("%d.%s", r.ec, r.lang)]; ok {
		return em
	}

	log.Printf("I18n: invalid code/lang [%d.%s]", r.ec, r.lang)
	return ""
}

func (r *reply) WithData(d interface{}) Reply {
	clone := *r
	clone.data = d
	return &clone
}

func (r *reply) reply() {}

type errReply struct {
	Reply
	err    error
	fields map[string]interface{}
}

func (er *errReply) reset() {
	er.Reply = nil 
	er.err = nil
	er.fields = nil
}

func (er *errReply) WithField(field string, value interface{}) ErrReply {
	if er.fields == nil {
		er.fields = make(map[string]interface{}, 1)
	}
	er.fields[field] = value
	return er
}

func (er *errReply) WithFields(fields map[string]interface{}) ErrReply {
	if er.fields == nil {
		er.fields = make(map[string]interface{}, len(fields))
	}

	for field, value := range fields {
		er.fields[field] = value
	}
	return er
}

func (er *errReply) WithErr(err error) ErrReply {
	multierr.AppendInto(&er.err, err)
	return er
}

func (r *errReply) Error() string {
	if r.err != nil {
		return fmt.Sprintf("%+v", errors.WithMessage(r.err, r.EM()))
	}
	return fmt.Sprintf("code: %d, %s", r.EC(), r.EM())
}

// 内部默认的几个状态码(可根据需要添加)

// OK success
var OK Reply = &reply{
	httpCode: http.StatusOK,
	ec:       200,
}

// ErrIllegalParams bad request 参数错误、服务端无法理解请求
var ErrIllegalparams Reply = &reply{
	httpCode: http.StatusBadRequest,
	ec:       400,
	em:       http.StatusText(http.StatusBadRequest),
}

// ErrUnauthorized unauthorized 无权限
var ErrUnauthorized Reply = &reply{
	httpCode: http.StatusUnauthorized,
	ec:       401,
	em:       http.StatusText(http.StatusUnauthorized),
}

// ErrNotFound 404 not found
var ErrNotFound Reply = &reply{
	httpCode: http.StatusNotFound,
	ec: 404,
	em: http.StatusText(http.StatusNotFound),
}

// ErrMethodNotAllowed method not allowed
var ErrMethodNotAllowed Reply = &reply{
	httpCode: http.StatusMethodNotAllowed,
	ec: 405,
	em: http.StatusText(http.StatusMethodNotAllowed),
}

// ErrInternal internal error 服务器内部错误
var ErrInternal Reply = &reply{
	httpCode: http.StatusInternalServerError,
	ec:       500,
	em:       http.StatusText(http.StatusInternalServerError),
}

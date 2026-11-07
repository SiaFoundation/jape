package selectresponse

import "go.sia.tech/jape"

var done = make(chan struct{})
var handlers = map[string]jape.Handler{
	"POST /select": func(jc jape.Context) {
		select {
		case <-done:
			jc.Error(nil, 503)
			return
		default:
		}
		jc.Encode("ok")
	},
	"POST /duplicate": func(jc jape.Context) {
		select {
		case <-done:
			jc.Error(nil, 503)
		default:
		}
		jc.Encode("ok") // want "Handler writes multiple responses"
	},
}

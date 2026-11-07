package jape

type Context struct{}

func (Context) Error(error, int)   {}
func (Context) Encode(interface{}) {}

type Handler func(Context)

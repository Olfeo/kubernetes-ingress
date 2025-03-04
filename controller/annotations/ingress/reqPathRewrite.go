package ingress

import (
	"fmt"
	"strings"

	"github.com/haproxytech/kubernetes-ingress/controller/annotations/common"
	"github.com/haproxytech/kubernetes-ingress/controller/haproxy/rules"
	"github.com/haproxytech/kubernetes-ingress/controller/store"
)

type ReqPathRewrite struct {
	name  string
	rules *rules.Rules
}

func NewReqPathRewrite(n string, r *rules.Rules) *ReqPathRewrite {
	return &ReqPathRewrite{name: n, rules: r}
}

func (a *ReqPathRewrite) GetName() string {
	return a.name
}

func (a *ReqPathRewrite) Process(k store.K8s, annotations ...map[string]string) (err error) {
	input := common.GetValue(a.GetName(), annotations...)
	if input == "" {
		return
	}
	parts := strings.Fields(strings.TrimSpace(input))

	var rewrite *rules.ReqPathRewrite
	switch len(parts) {
	case 1:
		rewrite = &rules.ReqPathRewrite{
			PathMatch: "(.*)",
			PathFmt:   parts[0],
		}
	case 2:
		rewrite = &rules.ReqPathRewrite{
			PathMatch: parts[0],
			PathFmt:   parts[1],
		}
	default:
		return fmt.Errorf("incorrect value '%s', path-rewrite takes 1 or 2 params ", input)
	}
	a.rules.Add(rewrite)
	return
}

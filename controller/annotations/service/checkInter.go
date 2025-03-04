package service

import (
	"github.com/haproxytech/client-native/v2/models"

	"github.com/haproxytech/kubernetes-ingress/controller/annotations/common"
	"github.com/haproxytech/kubernetes-ingress/controller/store"
	"github.com/haproxytech/kubernetes-ingress/controller/utils"
)

type CheckInter struct {
	name    string
	backend *models.Backend
}

func NewCheckInter(n string, b *models.Backend) *CheckInter {
	return &CheckInter{name: n, backend: b}
}

func (a *CheckInter) GetName() string {
	return a.name
}

func (a *CheckInter) Process(k store.K8s, annotations ...map[string]string) error {
	input := common.GetValue(a.GetName(), annotations...)
	if input == "" {
		a.backend.DefaultServer.Inter = nil
		return nil
	}
	value, err := utils.ParseTime(input)
	if err != nil {
		return err
	}
	a.backend.DefaultServer.Inter = value
	return nil
}

module go.xrstf.de/prow_exporter

go 1.16

require (
	github.com/onsi/gomega v1.16.0 // indirect
	github.com/prometheus/client_golang v1.11.0
	github.com/sirupsen/logrus v1.8.1
	go.uber.org/zap v1.19.0 // indirect
	golang.org/x/sys v0.0.0-20210903071746-97244b99971b // indirect
	k8s.io/apimachinery v0.22.1
	k8s.io/client-go v11.0.1-0.20190805182717-6502b5e7b1b5+incompatible
	k8s.io/test-infra v0.0.0-20210904105319-8696a0d106ad
	k8s.io/utils v0.0.0-20210820185131-d34e5cb4466e // indirect
)

replace k8s.io/client-go => k8s.io/client-go v0.22.1

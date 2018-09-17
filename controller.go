package main

import (
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	extensionsv1beta1 "k8s.io/api/extensions/v1beta1"
	"k8s.io/apimachinery/pkg/watch"
)

//HAProxyController is ingress controller
type HAProxyController struct {
	k8s        *K8s
	Namespaces map[string]*Namespace
	ConfigMap  map[string]*ConfigMap
}

// Start initialize and run HAProxyController
func (c *HAProxyController) Start() {

	k8s, err := GetKubernetesClient()
	if err != nil {
		log.Panic(err)
	}
	c.k8s = k8s

	_, nsWatch, err := k8s.GetNamespaces()
	if err != nil {
		log.Panic(err)
	}

	_, svcWatch, err := k8s.GetServices()
	if err != nil {
		log.Panic(err)
	}

	_, podWatch, err := k8s.GetPods()
	if err != nil {
		log.Panic(err)
	}

	_, ingressWatch, err := k8s.GetIngresses()
	if err != nil {
		log.Panic(err)
	}

	_, configMapWatch, err := k8s.GetConfigMap()
	if err != nil {
		log.Panic(err)
	}

	//TODO
	//configmap

	go c.watchChanges(nsWatch, svcWatch, podWatch, ingressWatch, configMapWatch)
}

func (c *HAProxyController) watchChanges(namespaces watch.Interface,
	services watch.Interface, pods watch.Interface, ingresses watch.Interface, configMapWatch watch.Interface) {
	syncEveryNSeconds := 5
	eventChan := make(chan SyncDataEvent, watch.DefaultChanSize)
	go c.SyncData(eventChan)
	for {
		select {
		case msg := <-namespaces.ResultChan():
			obj := msg.Object.(*corev1.Namespace)
			namespace := Namespace{
				Name:  obj.GetName(),
				Watch: obj.GetName() == "default",
				//Annotations
				Pods:      make(map[string]*Pod),
				Services:  make(map[string]*Service),
				Ingresses: make(map[string]*Ingress),
			}
			eventChan <- SyncDataEvent{SyncType: NAMESPACE, EventType: msg.Type, Namespace: &namespace}
		case msg := <-services.ResultChan():
			obj := msg.Object.(*corev1.Service)
			svc := Service{
				Name:      obj.GetName(),
				Namespace: obj.GetNamespace(),
				//ClusterIP:  "string",
				//ExternalIP: "string",
				Ports: obj.Spec.Ports,

				Annotations: obj.ObjectMeta.Annotations,
				Selector:    obj.Spec.Selector,
			}
			eventChan <- SyncDataEvent{SyncType: SERVICE, EventType: msg.Type, Service: &svc}
		case msg := <-pods.ResultChan():
			obj := msg.Object.(*corev1.Pod)
			//LogWatchEvent(msg.Type, POD, obj)
			pod := Pod{
				Name:      obj.GetName(),
				Namespace: obj.GetNamespace(),
				Labels:    obj.Labels,
				IP:        obj.Status.PodIP,
				Status:    obj.Status.Phase,
				//Port:      obj.Status. ? yes no, check
			}
			eventChan <- SyncDataEvent{SyncType: POD, EventType: msg.Type, Pod: &pod}
		case msg := <-ingresses.ResultChan():
			obj := msg.Object.(*extensionsv1beta1.Ingress)
			ingress := Ingress{
				Name:        obj.GetName(),
				Namespace:   obj.GetNamespace(),
				Annotations: obj.ObjectMeta.Annotations,
				Rules:       obj.Spec.Rules,
			}
			eventChan <- SyncDataEvent{SyncType: INGRESS, EventType: msg.Type, Ingress: &ingress}
		case msg := <-configMapWatch.ResultChan():
			obj := msg.Object.(*corev1.ConfigMap)
			//only config with name=haproxy-configmap is interesting
			if obj.ObjectMeta.GetName() == "haproxy-configmap" {
				configMap := ConfigMap{
					Name: obj.GetName(),
					Data: obj.Data,
				}
				eventChan <- SyncDataEvent{SyncType: CONFIGMAP, EventType: msg.Type, ConfigMap: &configMap}
			}
		case <-time.After(time.Duration(syncEveryNSeconds) * time.Second):
			//TODO syncEveryNSeconds sec is hardcoded, change that (annotation?)
			//do sync of data every syncEveryNSeconds sec
			eventChan <- SyncDataEvent{SyncType: COMMAND, EventType: watch.Added}
		}
	}
}

//SyncData gets all kubernetes changes, aggregates them and apply to HAProxy
func (c *HAProxyController) SyncData(jobChan <-chan SyncDataEvent) {
	hadChanges := false
	c.Namespaces = make(map[string]*Namespace)
	c.ConfigMap = make(map[string]*ConfigMap)
	for job := range jobChan {
		switch job.SyncType {
		case COMMAND:
			if hadChanges {
				log.Println("job processing", job.SyncType)
				c.UpdateHAProxy()
				hadChanges = false
			}
		case NAMESPACE:
			switch job.EventType {
			case watch.Added:
				ns := c.GetNamespace(job.Namespace.Name)
				log.Println("Namespace added", ns.Name)
				hadChanges = true
			}
		case INGRESS:
			switch job.EventType {
			case watch.Added:
				ns := c.GetNamespace(job.Ingress.Namespace)
				ns.Ingresses[job.Ingress.Name] = job.Ingress
				log.Println("Ingress added", job.Ingress.Name)
				hadChanges = true
			}
		case SERVICE:
			switch job.EventType {
			case watch.Added:
				ns := c.GetNamespace(job.Service.Namespace)
				log.Println("namespace:", ns)
				ns.Services[job.Service.Name] = job.Service
				log.Println("Service added", job.Service.Name)
				hadChanges = true
			}
		case POD:
			switch job.EventType {
			case watch.Modified:
				newPod := job.Pod
				ns := c.GetNamespace(job.Pod.Namespace)
				oldPod, ok := ns.Pods[job.Pod.Name]
				if !ok {
					//intentionally do not add it. TODO see if our idea of only watching is ok
					log.Println("Pod not registered with controller, cannot modify !", job.Pod.Name)
				}
				ns.Pods[job.Pod.Name] = newPod
				log.Println("Pod modified", job.Pod.Name, oldPod.Status, newPod.Status)
				hadChanges = true
			case watch.Added:
				ns := c.GetNamespace(job.Pod.Namespace)
				ns.Pods[job.Pod.Name] = job.Pod
				log.Println("Pod added", job.Pod.Name)
				hadChanges = true
			case watch.Deleted:
				ns := c.GetNamespace(job.Pod.Namespace)
				_, ok := ns.Pods[job.Pod.Name]
				if ok {
					delete(ns.Pods, job.Pod.Name)
				} else {
					log.Println("Pod not registered with controller, cannot delete !", job.Pod.Name)
				}
				log.Println("Pod deleted", job.Pod.Name)
				//update immediately
				c.UpdateHAProxy()
			}
		case CONFIGMAP:
			switch job.EventType {
			case watch.Added:
				c.ConfigMap[job.ConfigMap.Name] = job.ConfigMap
				log.Println("ConfigMap added", job.ConfigMap.Name)
				hadChanges = true
			}
		}
	}
}

func (c *HAProxyController) GetNamespace(name string) *Namespace {
	namespace, ok := c.Namespaces[name]
	if ok {
		return namespace
	}
	newNamespace := c.NewNamespace(name)
	c.Namespaces[name] = newNamespace
	return newNamespace
}

func (c *HAProxyController) NewNamespace(name string) *Namespace {
	namespace := Namespace{
		Name:  name,
		Watch: name == "default",
		//Annotations
		Pods:      make(map[string]*Pod),
		Services:  make(map[string]*Service),
		Ingresses: make(map[string]*Ingress),
	}
	return &namespace
}

//UpdateHAProxy this need to generate API call/calls for HAProxy API
//currently it only generates direct cfg file for checking
func (c *HAProxyController) UpdateHAProxy() {

	var frontend strings.Builder
	var acls strings.Builder
	var useBackend strings.Builder
	var backends strings.Builder
	createdBackends := make(map[string]bool)
	WriteBufferedString(&frontend, "frontend http\n", "    mode http\n    bind *:80\n")
	for _, namespace := range c.Namespaces {
		if !namespace.Watch {
			continue
		}
		for _, ingress := range namespace.Ingresses {
			for _, rule := range ingress.Rules {
				WriteBufferedString(&acls, "    acl host-", rule.Host, " var(txn.hdr_host) -i ", rule.Host)
				for _, path := range rule.HTTP.Paths {
					service, ok := namespace.Services[path.Backend.ServiceName]
					if !ok {
						log.Println("service", path.Backend.ServiceName, "does not exists")
						continue
					}
					//WriteBufferedString(&acls, " ", rule.Host, ":", "80", " ", "\n")

					//acls.WriteString("    acl host-foo.bar var(txn.hdr_host) -i foo.bar foo.bar:80 foo.bar:443\n")
					//"    acl host-foo.bar var(txn.hdr_host) -i foo.bar foo.bar:80 foo.bar:443\n")
					WriteBufferedString(&useBackend,
						"    use_backend ", namespace.Name, "-", path.Backend.ServiceName, "-", path.Backend.ServicePort.String(),
						" if host-", rule.Host, " { var(txn.path) -m beg ", path.Path, " }\n")
					//use_backend default-web-5858 if host-foo.bar { var(txn.path) -m beg /web }

					selector := service.Selector
					if len(selector) == 0 {
						log.Println("service", service.Name, "no selector")
						continue
					}
					backendName := namespace.Name + "-" + service.Name + "-" + path.Backend.ServicePort.String()
					_, ok = createdBackends[backendName]
					if !ok {
						WriteBufferedString(&backends,
							"backend ", backendName, "\n",
							"    mode http\n",
							"    balance leastconn\n")
						index := 0
						for _, pod := range namespace.Pods {
							//TODO what about state unknown, should we do something about it?
							if pod.Status == corev1.PodRunning && hasSelectors(selector, pod.Labels) {
								WriteBufferedString(&backends,
									"    server server000", strconv.Itoa(index), " ", pod.IP, ":", path.Backend.ServicePort.String(),
									" weight 1 check port ", path.Backend.ServicePort.String(), "\n")
								index++
							}
						}
						createdBackends[backendName] = true
					}
					backends.WriteString("\n")
				}
			}
		}
	}
	var config strings.Builder
	WriteBufferedString(&config, getGlobal(), "\n\n", getDefault(), "\n\n", frontend.String(), "\n", acls.String(), "\n", useBackend.String(), "\n\n", backends.String())
	cfg := config.String()
	//fmt.Println(cfg)
	os.MkdirAll("/etc/haproxy/", 0644)

	tmpfile, err := ioutil.TempFile("", "haproxy-*.cfg")
	if err != nil {
		log.Fatal(err)
	}

	if _, err := tmpfile.WriteString(cfg); err != nil {
		log.Println(err)
	}
	if err := tmpfile.Close(); err != nil {
		log.Println(err)
	}
	cfgPath := tmpfile.Name()

	log.Println("The file path : ", cfgPath)
	cmd := exec.Command("haproxy", "-c", "-f", cfgPath)
	log.Printf("Running command and waiting for it to finish...")
	err = cmd.Run()
	if err != nil {
		//there is no point of looking what because this controller will communicate with api
		log.Println("haproxy", "-c", "-f", cfgPath)
		log.Println("Command finished with error: %v", err)
	} else {
		log.Println("it looks as everything is ok with config")
	}

	//defer os.Remove(tmpfile.Name()) // clean up
}

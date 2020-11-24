package config_agent

import (
	"encoding/json"
	"github.com/eden-framework/client/client_srv_configuration"
	"github.com/eden-framework/courier/client"
	"github.com/eden-framework/plugin-event/event"
	"github.com/profzone/envconfig"
	"github.com/sirupsen/logrus"
	"io/ioutil"
	"os"
	"os/signal"
	"reflect"
	"syscall"
	"time"
)

const (
	DefaultHost           = "localhost"
	DefaultMode           = "http"
	DefaultPort           = 8810
	DefaultRequestTimeout = 5
	DefaultPullInterval   = 60
	DefaultStackName      = "eden"
	DefaultServiceName    = "srv-configuration"
	DefaultStoragePath    = "./config/raw_config"

	FirstRunInitTopic = "first-run-init"
	DiffConfigTopic   = "diff-config"
)

type Agent struct {
	Host               string
	Mode               string
	Port               int16
	Timeout            int64
	PullConfigInterval int64
	StackID            uint64
	StoragePath        string

	evt       *event.MessageBus
	client    *client_srv_configuration.ClientSrvConfiguration
	config    interface{}
	rawConfig []RawConfig
	configMap map[string]string
}

func (a *Agent) setDefaults() {
	if a.Host == "" {
		a.Host = DefaultHost
	}
	if a.Mode == "" {
		a.Mode = DefaultMode
	}
	if a.Port == 0 {
		a.Port = DefaultPort
	}
	if a.Timeout == 0 {
		a.Timeout = DefaultRequestTimeout
	}
	if a.PullConfigInterval == 0 {
		a.PullConfigInterval = DefaultPullInterval
	}
	if a.StoragePath == "" {
		a.StoragePath = DefaultStoragePath
	}
	if a.client == nil {
		c := &client_srv_configuration.ClientSrvConfiguration{
			Client: &client.Client{
				Host:    a.Host,
				Mode:    a.Mode,
				Port:    a.Port,
				Timeout: envconfig.Duration(time.Duration(a.Timeout) * time.Second),
			},
		}
		a.client = c
	}
	a.client.MarshalDefaults()
}

func (a *Agent) Init() {
	a.setDefaults()
	if a.StackID == 0 {
		panic("must specify a StackID")
	}
	a.rawConfig = make([]RawConfig, 0)
	a.configMap = make(map[string]string)
}

func (a *Agent) BindConf(conf interface{}) {
	t := reflect.TypeOf(conf)
	if t.Kind() != reflect.Ptr {
		panic("the conf to be bind is not pointer.")
	}
	a.config = conf
}

func (a *Agent) BindBus(bus *event.MessageBus) {
	a.evt = bus
}

func (a *Agent) Start() {
	if a.config == nil {
		panic("conf is not bind, please use BindConf to bind a configuration entry first.")
	}
	if a.evt == nil {
		panic("bus is not bind, please use BindBus to bind a MessageBus entry first.")
	}

	a.getFistRunConfig()
	_, _ = a.evt.AsyncPublish(event.Message{
		Topic: FirstRunInitTopic,
	})
	a.runtimeConfig()
}

func (a *Agent) runtimeConfig() {
	ticker := time.NewTicker(time.Duration(a.PullConfigInterval) * time.Second)
	quit := make(chan os.Signal)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM, syscall.SIGUSR2)

Run:
	for {
		select {
		case <-ticker.C:
			a.getRuntimeConfig()
		case <-quit:
			break Run
		}
	}
}

func (a *Agent) getFistRunConfig() {
	var result []byte
	var err error

	result, err = a.loadConfigFromService()
	if err == nil {
		_ = a.saveConfigToFile(result)
	} else {
		result, err = a.loadConfigFromFile()
	}

	if err != nil {
		logrus.Panicf("load configuration failed, neither remote or local. err: %v", err)
	}

	err = json.Unmarshal(result, &a.rawConfig)
	if err != nil {
		logrus.Panicf("unmarshal raw configuration err: %v", err)
	}

	for _, config := range a.rawConfig {
		a.configMap[config.Key] = config.Value
	}

	jsonConfig, err := json.Marshal(a.configMap)
	if err != nil {
		logrus.Panicf("marshal raw configuration err: %v", err)
	}

	err = json.Unmarshal(jsonConfig, a.config)
	if err != nil {
		logrus.Panicf("unmarshal configuration err: %v", err)
	}
}

func (a *Agent) getRuntimeConfig() {
	var result []byte
	var err error

	result, err = a.loadConfigFromService()
	if err == nil {
		_ = a.saveConfigToFile(result)
	} else {
		result, err = a.loadConfigFromFile()
	}

	if err != nil {
		logrus.Panicf("load configuration failed, neither remote or local. err: %v", err)
	}

	err = json.Unmarshal(result, &a.rawConfig)
	if err != nil {
		logrus.Panicf("unmarshal raw configuration err: %v", err)
	}

	currentConfigMap := make(map[string]string)
	for _, config := range a.rawConfig {
		currentConfigMap[config.Key] = config.Value
	}

	jsonConfig, err := json.Marshal(currentConfigMap)
	if err != nil {
		logrus.Panicf("marshal raw configuration err: %v", err)
	}

	err = json.Unmarshal(jsonConfig, a.config)
	if err != nil {
		logrus.Panicf("unmarshal configuration err: %v", err)
	}

	diff := a.diffConfig(currentConfigMap)
	if len(diff) > 0 {
		for _, v := range diff {
			a.configMap[v.Key] = v.Value
		}
		_, err = a.evt.AsyncPublish(event.Message{
			Topic: DiffConfigTopic,
			Data:  diff,
		})
	}
}

func (a *Agent) diffConfig(current map[string]string) (diff map[string]DiffConfig) {
	diff = make(map[string]DiffConfig)
	for key, val := range current {
		if v, ok := a.configMap[key]; ok {
			if v != val {
				// 存在key但是值发生改变
				diff[key] = DiffConfig{
					Key:   key,
					Value: val,
					Tag:   false,
				}
			}
		} else {
			// 不存在key
			diff[key] = DiffConfig{
				Key:   key,
				Value: val,
				Tag:   true,
			}
		}
	}

	return
}

func (a *Agent) loadConfigFromService() ([]byte, error) {
	request := client_srv_configuration.GetConfigurationsRequest{
		StackID: a.StackID,
	}
	resp, err := a.client.GetConfigurations(request)
	if err == nil {
		return json.Marshal(resp.Body)
	}
	return nil, err
}

func (a *Agent) loadConfigFromFile() ([]byte, error) {
	return ioutil.ReadFile(a.StoragePath)
}

func (a *Agent) saveConfigToFile(raw []byte) error {
	return ioutil.WriteFile(a.StoragePath, raw, os.ModePerm)
}

type DiffConfig struct {
	Key   string
	Value string
	Tag   bool
}

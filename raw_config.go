package config_agent

import "github.com/eden-framework/sqlx/datatypes"

type RawConfig struct {
	datatypes.PrimaryID
	// 业务ID
	ConfigurationID uint64 `json:"configurationID,string"`
	// StackID
	StackID uint64 `json:"stackID,string"`
	// Key
	Key string `json:"key"`
	// Value
	Value string `json:"value"`
	datatypes.OperateTime
}

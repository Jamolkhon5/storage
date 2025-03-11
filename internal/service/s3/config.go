package s3

import (
	"fmt"
	"github.com/spf13/viper"
)

type Config struct {
	AccessKeyID     string `mapstructure:"AccessKeyID"`
	SecretAccessKey string `mapstructure:"SecretAccessKey"`
	Bucket          string `mapstructure:"Bucket"`
}

func NewConfig(path string) (*Config, error) {
	viper.SetConfigFile(path)
	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("cannot read config from %s: %w", path, err)
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("cannot unmarshal config: %w", err)
	}

	// Проверяем, что все необходимые поля заполнены
	if cfg.AccessKeyID == "" {
		return nil, fmt.Errorf("AccessKeyID is required")
	}
	if cfg.SecretAccessKey == "" {
		return nil, fmt.Errorf("SecretAccessKey is required")
	}
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("Bucket is required")
	}

	return &cfg, nil
}

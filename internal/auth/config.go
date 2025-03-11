package auth

import (
	"fmt"
	"github.com/spf13/viper"
)

type Config struct {
	AuthAddr string `mapstructure:"AUTH"`
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

	return &cfg, nil
}

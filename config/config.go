package config

import (
	"log"
	"strings"

	"github.com/spf13/viper"
)

// Config Aggregate all configurations
type Config struct {
	Server   ServerConfig
	Database DatabaseConfig
	Chain    ChainConfig
}

type ServerConfig struct {
	Port string `mapstructure:"port"`
}

type DatabaseConfig struct {
	Dsn string `mapstructure:"dsn"`
}

type ChainConfig struct {
	RpcUrl          string `mapstructure:"rpc_url"`
	ContractAddress string `mapstructure:"contract_address"`
	StartBlock      int64  `mapstructure:"start_block"`
}

// LoadConfig
func LoadConfig() *Config {
	// 1. Set default values (to prevent configuration files from not being written)
	viper.SetDefault("server.port", "8080")
	viper.SetDefault("chain.start_block", 18000000)

	// 2. Tell Viper what the configuration file is called
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")

	// 3. Enable automatic reading of environment variables
	viper.SetEnvPrefix("APP")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	// 4. Read configuration file
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			log.Fatalf("Failed to read configuration file: %v", err)
		}
	}

	// 5. Map the read values to the structure
	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		log.Fatalf("Configuration parsing failed: %v", err)
	}

	return &config
}

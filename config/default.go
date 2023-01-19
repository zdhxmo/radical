package config 

import (
	"github.com/spf13/viper"
)

type Config struct {
    OpenAIKey   string  `mapstructure:"OPENAI_KEY"`
    GoogleKey   string  `mapstructure:"GOOGLE_KEY"`
    AssemblyKey string  `mapstructure:"ASSEMBLY_AI_KEY"`
}

func LoadConfig(path string) (config Config, err error) {
	viper.AddConfigPath(path)
	viper.SetConfigType("env")
	viper.SetConfigName("app")

	viper.AutomaticEnv()

	err = viper.ReadInConfig()
	if err != nil {
		return
	}

	err = viper.Unmarshal(&config)
	return
}

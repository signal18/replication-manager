package tools

import (
  "os"
  "strconv"
)

func SetValueFromEnv(field interface{}, envVar string) {

  env := os.Getenv(envVar)
  if len(env) > 0 {

    switch v := field.(type) { 
      case *int:
            *v, _ = strconv.Atoi(env)
      case *string:
            *v = env
      case *bool:
            *v, _ = strconv.ParseBool(env)
    } 
  }
}
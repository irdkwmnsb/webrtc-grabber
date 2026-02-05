package config

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"

	"gopkg.in/yaml.v3"
)

func LoadAppConfig(dir string) (*AppConfig, error) {
	cfg := DefaultAppConfig()

	var rawServer RawServerConfig
	if err := loadFileInto(dir, "server", &rawServer); err != nil {
		return nil, err
	}
	mergeInto(&cfg.Server, rawServer.ToDomain())

	var rawSec RawSecurityConfig
	if err := loadFileInto(dir, "security", &rawSec); err != nil {
		return nil, err
	}
	parsedSec, err := rawSec.ToDomain()
	if err != nil {
		return nil, err
	}
	mergeInto(&cfg.Security, parsedSec)

	var rawWebRTC RawWebRTCConfig
	if err := loadFileInto(dir, "webrtc", &rawWebRTC); err != nil {
		return nil, err
	}
	mergeInto(&cfg.WebRTC, rawWebRTC.ToDomain())

	var rawRecord RawRecordConfig
	if err := loadFileInto(dir, "record", &rawRecord); err != nil {
		return nil, err
	}
	mergeInto(&cfg.Record, rawRecord.ToDomain())

	return &cfg, nil
}

func loadFileInto(dir, filenameBase string, target interface{}) error {
	basePath := filepath.Join(dir, filenameBase)

	if f, err := os.Open(basePath + ".yaml"); err == nil {
		defer f.Close()
		if err := yaml.NewDecoder(f).Decode(target); err != nil {
			if errors.Is(err, io.EOF) {
				slog.Warn("config file is empty, using defaults", "file", basePath+".yaml")
				return nil
			}
			return err
		}
		return nil
	}

	if f, err := os.Open(basePath + ".json"); err == nil {
		defer f.Close()
		if err := json.NewDecoder(f).Decode(target); err != nil {
			if errors.Is(err, io.EOF) {
				slog.Warn("config file is empty, using defaults", "file", basePath+".json")
				return nil
			}
			return err
		}
		return nil
	}

	return nil
}

func mergeInto(dst, src interface{}) {
	dstVal := reflect.ValueOf(dst).Elem()
	srcVal := reflect.ValueOf(src)

	mergeValues(dstVal, srcVal)
}

func mergeValues(dstVal, srcVal reflect.Value) {
	for i := 0; i < srcVal.NumField(); i++ {
		srcField := srcVal.Field(i)
		dstField := dstVal.Field(i)

		switch srcField.Kind() {
		case reflect.Struct:
			mergeValues(dstField, srcField)
		case reflect.Slice:
			if !srcField.IsNil() && srcField.Len() > 0 {
				dstField.Set(srcField)
			}
		case reflect.Pointer:
			if !srcField.IsNil() {
				dstField.Set(srcField)
			}
		default:
			if !srcField.IsZero() {
				dstField.Set(srcField)
			}
		}
	}
}

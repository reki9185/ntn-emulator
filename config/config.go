package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v2"
)

// RANConfig represents the RAN configuration from ran.yaml
type RANConfig struct {
	GNB struct {
		AMFN2IP             string `yaml:"amfN2Ip"`
		RANN2IP             string `yaml:"ranN2Ip"`
		UPFN3IP             string `yaml:"upfN3Ip"`
		RANN3IP             string `yaml:"ranN3Ip"`
		RANDataPlaneIP      string `yaml:"ranDataPlaneIp"`
		AMFN2Port           int    `yaml:"amfN2Port"`
		RANN2Port           int    `yaml:"ranN2Port"`
		UPFN3Port           int    `yaml:"upfN3Port"`
		RANN3Port           int    `yaml:"ranN3Port"`
		RANControlPlanePort int    `yaml:"ranControlPlanePort"`
		RANDataPlanePort    int    `yaml:"ranDataPlanePort"`
		GNBID               string `yaml:"gnbId"`
		GNBName             string `yaml:"gnbName"`
		PLMNID              struct {
			MCC string `yaml:"mcc"`
			MNC string `yaml:"mnc"`
		} `yaml:"plmnId"`
		TAI struct {
			TAC             string `yaml:"tac"`
			BroadcastPLMNID struct {
				MCC string `yaml:"mcc"`
				MNC string `yaml:"mnc"`
			} `yaml:"broadcastPlmnId"`
		} `yaml:"tai"`
		SNSSAI struct {
			SST string `yaml:"sst"`
			SD  string `yaml:"sd"`
		} `yaml:"snssai"`
	} `yaml:"gnb"`
	Logger struct {
		Level string `yaml:"level"`
	} `yaml:"logger"`
}

// UEConfig represents the UE configuration from ue.yaml
type UEConfig struct {
	UE struct {
		UEIP                string `yaml:"ueIp"`
		AMFN2IP             string `yaml:"amfN2Ip"`
		AMFN2Port           int    `yaml:"amfN2Port"`
		UEN2IP              string `yaml:"ueN2Ip"`
		UEN2Port            int    `yaml:"ueN2Port"`
		RANControlPlaneIP   string `yaml:"ranControlPlaneIp"`
		RANControlPlanePort int    `yaml:"ranControlPlanePort"`
		RANDataPlaneIP      string `yaml:"ranDataPlaneIp"`
		RANDataPlanePort    int    `yaml:"ranDataPlanePort"`
		PLMNID              struct {
			MCC string `yaml:"mcc"`
			MNC string `yaml:"mnc"`
		} `yaml:"plmnId"`
		MSIN                       string `yaml:"msin"`
		AuthenticationSubscription struct {
			EncPermanentKey               string `yaml:"encPermanentKey"`
			EncOpcKey                     string `yaml:"encOpcKey"`
			AuthenticationManagementField string `yaml:"authenticationManagementField"`
			SequenceNumber                string `yaml:"sequenceNumber"`
		} `yaml:"authenticationSubscription"`
		IntegrityAlgorithm struct {
			NIA0 bool `yaml:"nia0"`
			NIA1 bool `yaml:"nia1"`
			NIA2 bool `yaml:"nia2"`
			NIA3 bool `yaml:"nia3"`
		} `yaml:"integrityAlgorithm"`
		CipheringAlgorithm struct {
			NEA0 bool `yaml:"nea0"`
			NEA1 bool `yaml:"nea1"`
			NEA2 bool `yaml:"nea2"`
			NEA3 bool `yaml:"nea3"`
		} `yaml:"cipheringAlgorithm"`
		PDUSession struct {
			DNN    string `yaml:"dnn"`
			SNSSAI struct {
				SST string `yaml:"sst"`
				SD  string `yaml:"sd"`
			} `yaml:"snssai"`
		} `yaml:"pduSession"`
		AccessType string `yaml:"accessType"`
		NRDC       struct {
			Enable bool `yaml:"enable"`
		} `yaml:"nrdc"`
		UETunnelDevice    string `yaml:"ueTunnelDevice"`
		IgnoreSetupTunnel bool   `yaml:"ignoreSetupTunnel"`
	} `yaml:"ue"`
	Logger struct {
		Level string `yaml:"level"`
	} `yaml:"logger"`
}

// LoadRANConfig loads RAN configuration from ran.yaml
func LoadRANConfig(path string) (*RANConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config RANConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &config, nil
}

// LoadUEConfig loads UE configuration from ue.yaml
func LoadUEConfig(path string) (*UEConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config UEConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &config, nil
}

// GetSUPI returns the SUPI (imsi-<mcc><mnc><msin>) from UE config
func (c *UEConfig) GetSUPI() string {
	return fmt.Sprintf("imsi-%s%s%s", c.UE.PLMNID.MCC, c.UE.PLMNID.MNC, c.UE.MSIN)
}

// GetIMSI returns the IMSI (<mcc><mnc><msin>) from UE config
func (c *UEConfig) GetIMSI() string {
	return fmt.Sprintf("%s%s%s", c.UE.PLMNID.MCC, c.UE.PLMNID.MNC, c.UE.MSIN)
}

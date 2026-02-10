package main

import (
	"fmt"
	"ntn-emulator/config"
	
	"github.com/free5gc/openapi/models"
)

func main() {
	// Load UE config
	ueCfg, err := config.LoadUEConfig("configs/ue.yaml")
	if err != nil {
		fmt.Printf("Failed to load config: %v\n", err)
		return
	}

	// Create AuthenticationSubscription like in cmd/ran.go
	authSubs := models.AuthenticationSubscription{
		AuthenticationMethod:          models.AuthMethod__5_G_AKA,
		EncPermanentKey:               ueCfg.UE.AuthenticationSubscription.EncPermanentKey,
		EncOpcKey:                     ueCfg.UE.AuthenticationSubscription.EncOpcKey,
		AuthenticationManagementField: ueCfg.UE.AuthenticationSubscription.AuthenticationManagementField,
		SequenceNumber: &models.SequenceNumber{
			Sqn: ueCfg.UE.AuthenticationSubscription.SequenceNumber,
		},
	}

	fmt.Printf("\n========== Authentication Subscription ==========\n")
	fmt.Printf("Auth Method:  %s\n", authSubs.AuthenticationMethod)
	fmt.Printf("K:            %s\n", authSubs.EncPermanentKey)
	fmt.Printf("OPC:          %s\n", authSubs.EncOpcKey)
	fmt.Printf("AMF:          %s\n", authSubs.AuthenticationManagementField)
	if authSubs.SequenceNumber != nil {
		fmt.Printf("SQN:          %s\n", authSubs.SequenceNumber.Sqn)
	}
	fmt.Printf("================================================\n\n")
}

#!/bin/bash

# Check and display subscriber configuration from free5GC MongoDB database

IMSI="${1:-208930000000001}"
SUPI="imsi-${IMSI}"

echo "========================================="
echo "Free5GC Subscriber Configuration Checker"
echo "========================================="
echo "Checking subscriber: $IMSI"
echo "SUPI: $SUPI"
echo ""

# Check if mongosh or mongo is available
if command -v mongosh &> /dev/null; then
    MONGO_CMD="mongosh"
elif command -v mongo &> /dev/null; then
    MONGO_CMD="mongo"
else
    echo "❌ Error: Neither mongosh nor mongo command found"
    echo "Please install MongoDB client tools"
    exit 1
fi

echo "Using MongoDB client: $MONGO_CMD"
echo ""

# Query the database
echo "Querying free5GC database..."
echo "========================================="

$MONGO_CMD --quiet free5gc --eval "
var result = db.subscriptionData.authenticationData.authenticationSubscription.findOne({ueId: '$SUPI'});
if (result) {
    print('✅ Subscriber found in database\\n');
    print('UE ID (SUPI): ' + result.ueId);
    print('');
    print('Database values:');
    print('  Permanent Key (K):   ' + (result.encPermanentKey || 'NOT SET'));
    print('  OPC Key:             ' + (result.encOpcKey || 'NOT SET'));
    print('  AMF:                 ' + (result.authenticationManagementField || 'NOT SET'));
    print('  Auth Method:         ' + (result.authenticationMethod || 'NOT SET'));
    print('  SQN:                 ' + (result.sequenceNumber ? result.sequenceNumber.sqn : 'NOT SET'));
    print('');
    print('Expected (from configs/ue.yaml):');
    print('  Permanent Key (K):   8baf473f2f8fd09487cccbd7097c6862');
    print('  OPC Key:             8e27b6af0e692e750f32667a3b14605d');
    print('  AMF:                 8000');
    print('  SQN:                 000000000030 (or higher)');
    print('');
    
    var k_match = result.encPermanentKey === '8baf473f2f8fd09487cccbd7097c6862';
    var opc_match = result.encOpcKey === '8e27b6af0e692e750f32667a3b14605d';
    var amf_match = result.authenticationManagementField === '8000';
    var db_sqn = result.sequenceNumber ? parseInt(result.sequenceNumber.sqn, 16) : 0;
    var config_sqn = parseInt('000000000030', 16);
    var sqn_ok = config_sqn > db_sqn;
    
    print('Validation:');
    print('  K matches:    ' + (k_match ? '✅' : '❌'));
    print('  OPC matches:  ' + (opc_match ? '✅' : '❌'));
    print('  AMF matches:  ' + (amf_match ? '✅' : '❌'));
    print('  SQN valid:    ' + (sqn_ok ? '✅ (config > database)' : '❌ (config must be > ' + result.sequenceNumber.sqn + ')'));
    print('');
    
    if (!k_match || !opc_match || !amf_match) {
        print('⚠️  KEY MISMATCH!');
        print('Database has different K/OPC/AMF values.');
        print('Fix: Update configs/ue.yaml to match database values above.');
    } else if (!sqn_ok) {
        print('⚠️  SQN TOO LOW!');
        print('Database SQN: ' + result.sequenceNumber.sqn);
        print('Config must be HIGHER than database.');
        print('');
        print('Fix: Edit configs/ue.yaml:');
        var next_sqn = (db_sqn + 1).toString(16).padStart(12, '0');
        print('  sequenceNumber: \"' + next_sqn + '\"');
        print('Then rebuild: go build -o /tmp/ntn_ran ./cmd/ran.go');
    } else {
        print('✅ All values correct! Ready to authenticate.');
    }
} else {
    print('❌ Subscriber NOT found!');
    print('Create in webconsole: http://localhost:5000');
}
print('');
"

echo ""
echo "To update SQN if out of sync:"
echo "  vi configs/ue.yaml  # Change sequenceNumber field"
echo "  go build -o /tmp/ntn_ran ./cmd/ran.go"

const { withInfoPlist } = require('expo/config-plugins');

function withReplayKitPlaceholder(config) {
  return withInfoPlist(config, (configWithInfoPlist) => {
    configWithInfoPlist.modResults.NRTReplayKitBridgeStatus = 'placeholder';
    configWithInfoPlist.modResults.NRTReplayKitBroadcastExtensionConfigured = false;
    return configWithInfoPlist;
  });
}

module.exports = withReplayKitPlaceholder;

require 'json'
package = JSON.parse(File.read(File.join(__dir__, 'package.json')))

Pod::Spec.new do |s|
  s.name = 'NarutoCapture'
  s.version = package['version']
  s.summary = package['description']
  s.description = package['description']
  s.license = package['license']
  s.author = package['author']
  s.homepage = package['homepage']
  s.platform = :ios, '16.4'
  s.source = { :git => 'https://github.com/expo/expo.git' }
  s.static_framework = true
  s.dependency 'ExpoModulesCore'
  s.source_files = 'ios/**/*.{h,m,mm,swift}'
  s.frameworks = 'AVKit', 'AVFoundation', 'ReplayKit', 'Network', 'CoreMedia', 'CoreVideo', 'ImageIO', 'UniformTypeIdentifiers'
  s.swift_version = '5.0'
  s.pod_target_xcconfig = {
    'DEFINES_MODULE' => 'YES',
    'SWIFT_COMPILATION_MODE' => 'wholemodule',
    'CLANG_CXX_LANGUAGE_STANDARD' => 'c++17'
  }
end

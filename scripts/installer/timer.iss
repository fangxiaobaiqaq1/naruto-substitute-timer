#ifndef AppVersion
  #error AppVersion is required
#endif
#ifndef SourceRoot
  #define SourceRoot AddBackslash(SourcePath) + "..\.."
#endif
#ifndef ReleaseDir
  #error ReleaseDir is required
#endif
[Setup]
AppId={{0A7B5ED9-57C5-4DE2-A1C0-25C8D671E5F3}
AppName=替身计时器
AppVersion={#AppVersion}
AppVerName=替身计时器 {#AppVersion}
AppPublisher=naruto-substitute-timer contributors
AppPublisherURL=https://github.com/fangxiaobaiqaq1/naruto-substitute-timer
AppUpdatesURL=https://github.com/fangxiaobaiqaq1/naruto-substitute-timer/releases
DefaultDirName={localappdata}\Programs\NarutoTimer
DefaultGroupName=替身计时器
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64
ArchitecturesInstallIn64BitMode=x64
MinVersion=10.0
WizardStyle=modern
WizardSizePercent=110
Compression=lzma2
SolidCompression=yes
OutputDir={#ReleaseDir}
OutputBaseFilename=naruto-timer-{#AppVersion}-setup-x64
UninstallDisplayIcon={app}\timer-app.exe
CloseApplications=yes
RestartApplications=no
SetupLogging=yes
LicenseFile={#SourceRoot}\LICENSE
InfoAfterFile={#SourceRoot}\scripts\installer\welcome.md
[Languages]
Name: "zh"; MessagesFile: "{#SourceRoot}\scripts\installer\ChineseSimplified.isl"
[Tasks]
Name: "desktopicon"; Description: "创建桌面快捷方式"; GroupDescription: "快捷方式"
[Files]
Source: "{#ReleaseDir}\timer-app.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceRoot}\scripts\installer\installed.marker"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceRoot}\LICENSE"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceRoot}\THIRD_PARTY_NOTICES.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#ReleaseDir}\licenses\*"; DestDir: "{app}\licenses"; Flags: ignoreversion recursesubdirs createallsubdirs
[Icons]
Name: "{group}\替身计时器"; Filename: "{app}\timer-app.exe"
Name: "{group}\卸载替身计时器"; Filename: "{uninstallexe}"
Name: "{autodesktop}\替身计时器"; Filename: "{app}\timer-app.exe"; Tasks: desktopicon
[Run]
Filename: "{app}\timer-app.exe"; Parameters: "--about"; Description: "打开替身计时器"; Flags: nowait postinstall skipifsilent
[UninstallDelete]
Type: files; Name: "{app}\timer-app.exe.previous"

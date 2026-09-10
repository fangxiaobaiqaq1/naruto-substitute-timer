# Microsoft Visual C++ runtime for the local OCR worker

These four unmodified x64 DLLs come from the official Visual Studio Community 2026 stable-channel developer component `Microsoft.VC.14.51.CRT.Redist.X64.base`, version 14.51.36247. Their archive members are under `Contents/VC/Redist/MSVC/14.51.36231/x64/Microsoft.VC145.CRT/`; the DLL file version is 14.51.36247.0. All four match the corresponding files in Microsoft's standalone Visual C++ v14 Redistributable byte for byte, and retain valid Microsoft Corporation Authenticode signatures. They complete the non-Windows dependency closure of the embedded ONNX Runtime 1.27.0 CPU DLL; the remaining dependencies are Windows system libraries and the Universal CRT supplied by Windows 10/11.

| File | Bytes | SHA-256 |
| --- | ---: | --- |
| `vcruntime140.dll` | 178,616 | `d1f4225df2cd877dbf130d5668a021dce3f94118455ff5ec952061c30afc9ce7` |
| `vcruntime140_1.dll` | 50,112 | `a7146c08f89fe5b04541ab507cdb59ff7b44534d4ba3c668a426c6450a03434e` |
| `msvcp140.dll` | 643,512 | `7c26614e1d733892c2deac7e245ce115504b1d80592dd0a01b08e3e5a55f89ca` |
| `msvcp140_1.dll` | 35,768 | `206c931bf90fdad8816de3b5e2ef80b2bcaa9406c89ecc05fe6fddffe251e982` |

Source URLs, catalog and component hashes, archive member paths, independent installer verification, and license source hashes are recorded in [provenance.json](provenance.json). The official stable Community bootstrapper identifies the product channel; its catalog identifies the exact developer component and expected SHA-256. Only the required component was downloaded and extracted for this MIT open-source project. No Visual Studio installer, layout operation, activation, or global runtime installation was executed. No files were copied from this machine's System32 directory. The complete installer is not embedded or executed at application runtime.

## Microsoft license terms

These files are Microsoft proprietary redistributable components, not MIT-licensed project code. The complete applicable documents are retained here:

- [VS-COMMUNITY-LICENSE.md](VS-COMMUNITY-LICENSE.md): all paragraph text from Microsoft's Community 2026 developer license, dated October 1, 2025. Its installation/use terms cover individual developers and OSI-approved open-source applications; its Distributable Code section permits object-code distribution subject to the stated requirements and restrictions.
- [VS-COMMUNITY-REDIST.md](VS-COMMUNITY-REDIST.md): the complete official REDIST list retrieved from Microsoft Learn. Its Visual C++ Runtime Files section covers the unmodified release files under `VC/Redist`, including this component's archive paths, and excludes debug files.
- [VC-RUNTIME-LICENSE.md](VC-RUNTIME-LICENSE.md): all paragraph text from the separate Microsoft runtime end-user terms, dated October 1, 2025.

The Community developer terms and REDIST list establish the distribution conditions; the runtime end-user terms alone do not. The Community terms do not state that running an installer, signing in, or activating the IDE is a prerequisite for the Distributable Code grant. This project obtained the official developer component directly for its open-source development. The copyright and notices must be retained, and the Microsoft components must remain under their Microsoft terms. Distribution with this application is subject to those terms; the project MIT license does not replace or expand them. See also [Microsoft's deployment and licensing guidance](https://learn.microsoft.com/en-us/cpp/windows/redistributing-visual-cpp-files).

The single-file Windows executable embeds this directory's Markdown notices and JSON provenance together with the DLLs. At runtime it writes the notices under `licenses/` in the same versioned OCR cache directory. Release ZIPs also carry the documents under `licenses/local-ocr/vcruntime/`, so the Microsoft terms remain available with either distribution format.

## Updating

Obtain a stable x64 package from [Microsoft's supported downloads](https://learn.microsoft.com/en-us/cpp/windows/latest-supported-vc-redist). Pin its resolved URL and SHA-256, verify Microsoft signatures, and extract the original files without modification. Update all four as a set; recheck the native DLL dependency closure, license terms, and both a clean-cache and conflicting-app-local-runtime worker start. App-local runtime files are serviced by application updates, not by the machine's separately installed Visual C++ Redistributable.

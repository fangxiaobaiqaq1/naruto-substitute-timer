// Compile the actual Objective-C++ adapter on the macOS runner as well as iOS.
// Including the implementation lets this small fixture exercise its private
// decoder without adding a production testing API.
#import "../RecognitionBridge.mm"
#include <cassert>
#include <cstdio>

int main() {
    @autoreleasepool {
        // PNG encoded independently, top row red/green, bottom blue/half-alpha white.
        NSData *png = [[NSData alloc] initWithBase64EncodedString:@"iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAE0lEQVR4nGP4z8DwHwyBNAg0AABJSQl4KKDbdwAAAABJRU5ErkJggg==" options:0];
        const uint8_t expected[] = {255,0,0,255, 0,255,0,255, 0,0,255,255, 255,255,255,128};
        auto& store = nt::AssetStore::Instance();
        store.AddImage("test/reference.png", 2, 2, expected, 8);
        decodePNGAsset("test/decoded.png", png, 2, 2);
        const auto actual = store.Image("test/decoded.png"), reference = store.Image("test/reference.png");
        assert(actual && actual->Width == 2 && actual->Height == 2);
        for (int y = 0; y < 2; ++y) for (int x = 0; x < 2; ++x) {
            assert(actual->Luma.GrayAt(x, y) == reference->Luma.GrayAt(x, y));
            assert(actual->Opaque(x, y) == reference->Opaque(x, y));
        }
        bool rejected = false;
        try { decodePNGAsset("test/wrong.png", png, 3, 2); }
        catch (const std::exception&) { rejected = true; }
        assert(rejected && !store.Has("test/wrong.png"));
        std::puts("PASS Apple adapter compile + PNG top-down rows, alpha and size validation");
    }
}

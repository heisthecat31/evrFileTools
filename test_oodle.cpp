#include <iostream>
#include <fstream>
#include <vector>
#include <windows.h>
#include <stdint.h>

typedef void (*InitType)();
typedef int64_t (*DecompressType)(const void*, int64_t, void*, int64_t, int, int, int, void*, int64_t, void*, void*, void*, int64_t, int);

int main() {
    HMODULE hDll = LoadLibraryA("J:\\Lone Echo v3.17.4 -ARMGDDN\\Lone Echo\\bin\\win7\\oodle_11_win64.dll");
    if (!hDll) {
        std::cout << "Failed to load DLL\n";
        return 1;
    }

    InitType init = (InitType)GetProcAddress(hDll, "Oodle_Init");
    DecompressType decompress = (DecompressType)GetProcAddress(hDll, "OodleLZ_Decompress");

    if (!init || !decompress) {
        std::cout << "Failed to find functions\n";
        return 1;
    }

    std::cout << "Calling Init\n";
    init();
    std::cout << "Init done\n";

    std::ifstream file("J:\\Lone Echo v3.17.4 -ARMGDDN\\Lone Echo\\_data\\5828984418\\win7\\primary\\117d2b6509c8ff79\\v4461094006\\0f5ed1a48da5c6ad", std::ios::binary);
    if (!file) return 1;

    file.seekg(0, std::ios::end);
    size_t size = file.tellg();
    file.seekg(0, std::ios::beg);

    std::vector<uint8_t> data(size);
    file.read((char*)data.data(), size);

    int64_t decompSize = *(uint64_t*)&data[8];
    int64_t compSize = *(uint64_t*)&data[16];

    std::vector<uint8_t> out(decompSize);

    int64_t res = decompress(&data[24], compSize, out.data(), decompSize, 1, 0, 0, nullptr, 0, nullptr, nullptr, nullptr, 0, 3);
    std::cout << "Decompressed size: " << res << "\n";

    return 0;
}

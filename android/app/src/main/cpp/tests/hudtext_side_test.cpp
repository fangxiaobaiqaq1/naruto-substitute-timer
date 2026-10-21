// Host tests of the account-text side rule (nt/hudtext.h DecideSide, the Windows rule of Go
// internal/hudtext Engine.Analyze): 我的名字 on exactly one side, else (ours covered) the side
// opposite the one complete account that is not mine. Run tests/hudtext_side_test.sh.
#include <array>
#include <cstdio>
#include <cstdlib>
#include <string>
#include <vector>

#include "nt/hudtext.h"

namespace {

int checks = 0;
void check(bool ok, const std::string& message) {
    checks++;
    if (!ok) {
        std::fprintf(stderr, "FAIL: %s\n", message.c_str());
        std::exit(1);
    }
}

// The recognizer's dictionary: everything but the decorative ღ.
bool known(char32_t r) { return r != U'ღ'; }

void expect(const std::array<std::string, 2>& accounts, const std::vector<std::string>& names, int side,
            const std::string& player, const std::string& opp, const std::string& how) {
    const nt::hudtext::SideDecision d = nt::hudtext::DecideSide(accounts, names, known);
    const std::string what = "[" + accounts[0] + " | " + accounts[1] + "] names=" + std::to_string(names.size()) +
                             (names.empty() ? "" : " " + names[0]);
    check(d.side == side, what + ": side " + std::to_string(d.side) + ", want " + std::to_string(side));
    check(d.player == player, what + ": player \"" + d.player + "\", want \"" + player + "\"");
    check(d.opp == opp, what + ": opp \"" + d.opp + "\", want \"" + opp + "\"");
    check(d.how == how, what + ": how \"" + d.how + "\", want \"" + how + "\"");
}

const std::string kElim = "other account not mine";

void elimination() {
    const std::vector<std::string> mine{"白方小"};
    // Ours unread (covered), one complete account that is not mine: we are on the other side.
    expect({"", "(王子不是我)"}, mine, 0, "", "王子不是我", kElim);
    expect({"(爱有天意9)", ""}, mine, 1, "", "爱有天意9", kElim);
    // Ours covered so far that not even its start is readable as mine (a mistyped name): same.
    expect({"(白方", "(爱有天意9)"}, {"张三"}, 0, "", "爱有天意9", kElim);
    expect({"(爱有天意9)", "(白方"}, {"张三"}, 1, "", "爱有天意9", kElim);
    // Both complete and not mine (a mistyped name with both titles read): no side.
    expect({"(白方小)", "(王子不是我)"}, {"张三"}, -1, "", "", "");
    // A covered end is not a complete account: neither side is proof.
    expect({"(白方", "(王子不"}, {"张三"}, -1, "", "", "");
    // An account that fails validAccount proves nothing.
    expect({"", "(王子/不是我)"}, mine, -1, "", "", "");
    // Nothing read.
    expect({"", ""}, mine, -1, "", "", "");
}

void mineFirst() {
    const std::vector<std::string> mine{"白方小"};
    // The covered start of mine wins and names us (PlayerName set, not the elimination).
    expect({"(白方", "(王子不是我)"}, mine, 0, "白方小", "王子不是我", "account start, end covered");
    expect({"(王子不是我)", "(白方"}, mine, 1, "白方小", "王子不是我", "account start, end covered");
    // A complete account beats a covered start; the other side is the opponent.
    expect({"(白方小)", "(王子不是我)"}, mine, 0, "白方小", "王子不是我", "account");
    expect({"(白方", "(白方小)"}, mine, 1, "白方小", "", "account");
    // Both sides look like mine: no side (complete or covered alike).
    expect({"(白方小)", "(白方小)"}, mine, -1, "", "", "");
    expect({"(白方", "(白方小"}, mine, -1, "", "", "");
    // A decorative character the recognizer cannot output matches one other character: mine,
    // not "an account that is not mine".
    expect({"(白方", "(爱有天意9)"}, {"爱有天意ღ"}, 1, "爱有天意ღ", "", "account");
    // Several names: the one shown.
    expect({"", "(王子不是我)"}, {"张三", "王子不是我"}, 1, "王子不是我", "", "account");
}

void namesRequired() {
    expect({"", "(王子不是我)"}, {}, -1, "", "", "");
    expect({"", "(王子不是我)"}, {""}, -1, "", "", "");
    expect({"(白方小)", ""}, {}, -1, "", "", "");
}

}  // namespace

int main() {
    elimination();
    mineFirst();
    namesRequired();
    std::printf("hudtext_side_test: %d checks passed\n", checks);
    return 0;
}

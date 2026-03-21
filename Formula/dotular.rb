# typed: false
# frozen_string_literal: true

# This formula is automatically updated by GoReleaser on each release.
# To install: brew tap atomikpanda/dotular https://github.com/atomikpanda/dotular && brew install dotular
class Dotular < Formula
  desc "A config-driven dotfile manager"
  homepage "https://github.com/atomikpanda/dotular"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/atomikpanda/dotular/releases/download/v0.1.0/dotular_0.1.0_darwin_arm64.tar.gz"
      sha256 "PLACEHOLDER"
    end
    on_intel do
      url "https://github.com/atomikpanda/dotular/releases/download/v0.1.0/dotular_0.1.0_darwin_amd64.tar.gz"
      sha256 "PLACEHOLDER"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/atomikpanda/dotular/releases/download/v0.1.0/dotular_0.1.0_linux_arm64.tar.gz"
      sha256 "PLACEHOLDER"
    end
    on_intel do
      url "https://github.com/atomikpanda/dotular/releases/download/v0.1.0/dotular_0.1.0_linux_amd64.tar.gz"
      sha256 "PLACEHOLDER"
    end
  end

  def install
    bin.install "dotular"
  end

  test do
    system "#{bin}/dotular", "platform"
  end
end

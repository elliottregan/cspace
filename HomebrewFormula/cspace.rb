# This is a reference formula. The actual Homebrew formula is auto-generated
# by GoReleaser and pushed to the elliottregan/homebrew-cspace tap repository.
#
# To install cspace via Homebrew:
#   brew tap elliottregan/cspace
#   brew install cspace
#
# To install without a tap (using this formula directly):
#   brew install elliottregan/cspace/cspace

class Cspace < Formula
  desc "Portable CLI for managing Claude Code devcontainer instances"
  homepage "https://github.com/elliottregan/cspace"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/elliottregan/cspace/releases/latest/download/cspace_darwin_arm64.zip"
    end
    on_intel do
      url "https://github.com/elliottregan/cspace/releases/latest/download/cspace_darwin_amd64.zip"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/elliottregan/cspace/releases/latest/download/cspace_linux_arm64.tar.gz"
    end
    on_intel do
      url "https://github.com/elliottregan/cspace/releases/latest/download/cspace_linux_amd64.tar.gz"
    end
  end

  def install
    bin.install "cspace"
  end

  test do
    system "#{bin}/cspace", "version"
  end
end

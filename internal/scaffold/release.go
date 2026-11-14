package scaffold

// publishWorkflowFile installs a thin, version-pinned caller for the shared
// registry publishing workflow. No registry credentials are stored in the repository.
func publishWorkflowFile() File {
	return File{
		Path: ".github/workflows/release.yml",
		Data: []byte(`name: Publish OCI images

on:
  release:
    types: [published]

permissions:
  contents: read
  packages: write

jobs:
  publish:
    uses: jmeiracorbal/docker-blue-green/.github/workflows/publish-images.yml@3efe0393c55e23f6a31e682015d0087f3453754a
    with:
      version: ${{ github.event.release.tag_name }}
      source_ref: ${{ github.event.release.tag_name }}
      engine_ref: 3efe0393c55e23f6a31e682015d0087f3453754a
    secrets:
      OCI_REGISTRY_TOKEN: ${{ secrets.OCI_REGISTRY_TOKEN }}

  deploy:
    needs: publish
    uses: jmeiracorbal/docker-blue-green/.github/workflows/deploy-remote.yml@3efe0393c55e23f6a31e682015d0087f3453754a
    with:
      version: ${{ github.event.release.tag_name }}
      source_ref: ${{ github.event.release.tag_name }}
      engine_ref: 3efe0393c55e23f6a31e682015d0087f3453754a
    secrets:
      SERVER_SSH_PRIVATE_KEY: ${{ secrets.SERVER_SSH_PRIVATE_KEY }}
      OCI_REGISTRY_PULL_USERNAME: ${{ secrets.OCI_REGISTRY_PULL_USERNAME }}
      OCI_REGISTRY_PULL_TOKEN: ${{ secrets.OCI_REGISTRY_PULL_TOKEN }}
`),
	}
}

using Http;
using OpenAPI;
using Versioning;

namespace PetStore;

@route("/pets")
namespace Pets {
  @get
  op listPets(
    @query
    @minValue(1)
    @maxValue(200)
    @extension("x-oapi-codegen-extra-tags", #{ validate: "omitempty,gte=1,lte=200" })
    limit?: int32,

    @query cursor?: string,
  ): {
    @statusCode statusCode: 200;
    @body body: PetPage;
  };

  @get
  op getPet(
    @path
    @minValue(1)
    petId: int64,
  ): {
    @statusCode statusCode: 200;
    @body body: Pet;
  } | {
    @statusCode statusCode: 404;
    @header contentType: "application/problem+json";
    @body body: Problem;
  };

  @post
  op createPet(@body body: NewPet): {
    @statusCode statusCode: 201;
    @body body: Pet;
  } | {
    @statusCode statusCode: 400;
    @header contentType: "application/problem+json";
    @body body: Problem;
  };

  @put
  op updatePet(
    @path
    @minValue(1)
    petId: int64,

    @body body: UpdatePet,
  ): {
    @statusCode statusCode: 200;
    @body body: Pet;
  } | {
    @statusCode statusCode: 404;
    @header contentType: "application/problem+json";
    @body body: Problem;
  } | {
    @statusCode statusCode: 400;
    @header contentType: "application/problem+json";
    @body body: Problem;
  };

  @delete
  op deletePet(
    @path
    @minValue(1)
    petId: int64,
  ): {
    @statusCode statusCode: 204;
  } | {
    @statusCode statusCode: 404;
    @header contentType: "application/problem+json";
    @body body: Problem;
  };

  @added(Versions.v2)
  @route("{petId}/tags")
  @get
  op listPetTags(
    @path
    @minValue(1)
    petId: int64,
  ): {
    @statusCode statusCode: 200;
    @body body: string[];
  } | {
    @statusCode statusCode: 404;
    @header contentType: "application/problem+json";
    @body body: Problem;
  };
}

@route("/health")
namespace Health {
  @get
  op healthCheck(): {
    @statusCode statusCode: 200;
    @body body: {
      status: string;
    };
  };
}

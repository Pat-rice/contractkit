using Http;
using OpenAPI;
using Versioning;

namespace PetStore;

enum PetKind {
  dog,
  cat,
  fish,
  bird,
  reptile,
}

model Pet {
  id: int64;
  name: string;
  kind: PetKind;
  age: int32;
  createdAt: utcDateTime;
  updatedAt: utcDateTime;

  @added(Versions.v2)
  tags: string[];
}

model NewPet {
  @minLength(1)
  @maxLength(255)
  @extension("x-oapi-codegen-extra-tags", #{ validate: "required,min=1,max=255" })
  name: string;

  @extension("x-oapi-codegen-extra-tags",
    #{ validate: "required,oneof=dog cat fish bird reptile" })
  kind: PetKind;

  @minValue(0)
  @maxValue(100)
  @extension("x-oapi-codegen-extra-tags", #{ validate: "gte=0,lte=100" })
  age: int32;

  @added(Versions.v2)
  @extension("x-oapi-codegen-extra-tags",
    #{ validate: "omitempty,dive,min=1,max=64" })
  tags?: string[];
}

model UpdatePet {
  @minLength(1)
  @maxLength(255)
  @extension("x-oapi-codegen-extra-tags", #{ validate: "omitempty,min=1,max=255" })
  name?: string;

  @extension("x-oapi-codegen-extra-tags",
    #{ validate: "omitempty,oneof=dog cat fish bird reptile" })
  kind?: PetKind;

  @minValue(0)
  @maxValue(100)
  @extension("x-oapi-codegen-extra-tags", #{ validate: "omitempty,gte=0,lte=100" })
  age?: int32;

  @added(Versions.v2)
  @extension("x-oapi-codegen-extra-tags",
    #{ validate: "omitempty,dive,min=1,max=64" })
  tags?: string[];
}

model PetPage {
  items: Pet[];
  nextCursor?: string;
}

model ValidationDetail {
  field: string;
  rule: string;
  message: string;
}

model Error {
  code: string;
  message: string;
  details?: ValidationDetail[];
}

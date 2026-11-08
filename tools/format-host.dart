import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:archive/archive.dart';
import 'package:crypto/crypto.dart';
import 'package:starry_character_card_codec/starry_character_card_codec.dart';
import 'package:starry_domain_entities/starry_domain_entities.dart';
import 'package:starry_prompt_compiler/starry_prompt_compiler.dart';

// Invoked only by the CLI in a private staging directory. No HTTP, credentials,
// database access or archive extraction is performed by this process.
Future<void> main(List<String> args) async {
  try {
    if (args.length != 4) throw const FormatException('Invalid host arguments');
    final action = args[0], type = args[1], input = args[2], output = args[3];
    if (action == 'inspect') {
      final graph = await inspect(type, input, output);
      await File('$output/graph.json').writeAsString(jsonEncode(graph));
    } else if (action == 'export') {
      final graph =
          jsonDecode(await File(input).readAsString()) as Map<String, dynamic>;
      await exportGraph(type, graph, output);
    } else {
      throw const FormatException('Unknown host action');
    }
    stdout.writeln('{"state":"complete"}');
  } on Object {
    // Source text and exceptions can contain private prompts or filesystem data.
    stderr.writeln('{"error":"format_conversion_failed"}');
    exitCode = 1;
  }
}

Map<String, dynamic> characterFields(CharacterDraft d) => {
  'id': d.id,
  'name': d.name,
  'nickname': d.nickname,
  'description': d.description,
  'personality': d.personality,
  'scenario': d.scenario,
  'first_message': d.firstMessage,
  'example_messages': d.exampleMessages,
  'alternate_greetings': d.alternateGreetings,
  'system_prompt': d.systemPrompt,
  'post_history_instructions': d.historyInstructions,
  'depth_prompt': d.depthPrompt?.toJson(),
  'talkativeness': d.talkativeness,
  'creator_notes': d.creatorNotes,
  'character_version': d.characterVersion,
  'tags': d.tags,
};

Map<String, dynamic> bookFields(WorldBookDraft d) => {
  'id': d.id,
  'name': d.name,
  'description': d.description,
  'scan_depth': d.scanDepth,
  'token_budget': d.tokenBudget,
  'recursive_scanning': d.recursiveScanning,
  'max_recursion_depth': d.maxRecursionDepth,
  'allow_entry_cascade': d.allowEntryCascade,
  'tags': d.tags,
  'creator_notes': d.creatorNotes,
  'extensions': d.extensions,
  'entries': d.entries.map((e) => e.toJson()).toList(),
};

Future<Map<String, dynamic>> inspect(
  String type,
  String input,
  String stage,
) async {
  final media = <Map<String, dynamic>>[];
  final books = <Map<String, dynamic>>[];
  final bindings = <Map<String, dynamic>>[];
  final relations = <Map<String, dynamic>>[];
  final warnings = <String>[];
  final characters = <Map<String, dynamic>>[];
  final skills = <Map<String, dynamic>>[];
  final skillBindings = <Map<String, dynamic>>[];
  final collectionItems = <Map<String, dynamic>>[];
  final origins = <String, Map<String, Object?>>{};
  Map<String, dynamic>? collection;
  Map<String, dynamic>? character;
  if (type == 'worldbook') {
    final p = await CardParseService().parseWorldBookFile(input);
    books.add(bookFields(p.draft));
    warnings.addAll(p.warnings);
  } else if (type == 'character' && !input.toLowerCase().endsWith('.starry')) {
    final p = await CardParseService().parseCharacterFile(input);
    character = characterFields(p.draft);
    if (p.draft.embeddedWorldBookJson case final embedded?) {
      // An explicitly attached book remains a dependency even without entries.
      final book =
          p.embeddedWorldBook ??
          CardParseService().parseWorldBookJson(jsonEncode(embedded)).draft;
      books.add(bookFields(book));
      bindings.add({'worldbook_id': book.id, 'reference_type': 'embedded'});
    }
    if (p.avatarBytes case final bytes?) {
      final hash = sha256.convert(bytes).toString();
      await File('$stage/$hash').writeAsBytes(bytes);
      media.add({'sha256': hash, 'file': '$stage/$hash'});
      relations.add({
        'role': 'avatar',
        'target_type': 'asset',
        'target_id': hash,
      });
    }
    warnings.add(
      'Original source retained separately; third-party character extensions are not trusted Catalog fields.',
    );
  } else if (type == 'character') {
    final p = await CanonicalCharacterPackageParseService().parse(input);
    if (p.live2dAliasMappings.isNotEmpty ||
        p.live2dBindings.isNotEmpty ||
        p.layeredSpriteMappings.isNotEmpty) {
      throw const FormatException('Unsupported presentation mappings');
    }
    final archive = ZipDecoder().decodeBytes(await File(input).readAsBytes());
    final preset = object(
      jsonDecode(
        utf8.decode(
          archive.files
                  .singleWhere((file) => file.name == kStarryPresetJsonPath)
                  .content
              as List<int>,
        ),
      ),
    );
    requireDefaultPreset(preset, p.character.name);
    if (p.authorship != null) {
      throw const FormatException(
        'Authorship needs the full application importer',
      );
    }
    character = p.character.toJson();
    books.addAll(p.worldbooks.map((b) => b.toJson()));
    bindings.addAll(p.worldbookBindings.map((b) => b.toJson()));
    relations.addAll(p.relations.map((r) => r.toJson()));
    skills.addAll(p.skills.map((s) => s.toJson()));
    skillBindings.addAll(p.skillBindings.map((b) => b.toJson()));
    origins.addAll(p.acquisitionOrigins);
    for (final m in p.packagedMedia) {
      await File('$stage/${m.contentHash}').writeAsBytes(m.bytes);
      media.add({'sha256': m.contentHash, 'file': '$stage/${m.contentHash}'});
    }
  } else if (type == 'skill') {
    const codec = StarrySkillPackageCodec();
    final bytes = await File(input).readAsBytes();
    final package =
        !input.toLowerCase().endsWith('.md') &&
        ZipDecoder()
            .decodeBytes(bytes)
            .files
            .any((file) => file.name == kStarryManifestPath);
    final skill = package
        ? codec.decode(bytes)
        : codec.importFile(bytes, fileName: input, id: 'imported-skill');
    skills.add(skill.toJson());
    if (package) {
      origins.addAll(codec.acquisitionOrigins(bytes));
    }
  } else if (type == 'collection') {
    final package = const StarryCollectionPackageCodec().decode(
      await File(input).readAsBytes(),
    );
    collection = package.collection.toJson();
    collectionItems.addAll(package.items.map((item) => item.toJson()));
    origins.addAll(package.acquisitionOrigins);
    final entries = <Map<String, dynamic>>[];
    final presets = <String, String>{};
    for (final resource in package.resources) {
      final data = resource.data;
      switch (resource.resourceType) {
        case 'character':
          characters.add(
            {...object(data['payload']), ...data}..remove('payload'),
          );
        case 'worldbook':
          books.add(
            {
              ...object(data['payload']),
              ...data,
              'entries': <Map<String, dynamic>>[],
            }..remove('payload'),
          );
        case 'worldbook_entry':
          entries.add(data);
        case 'character_worldbook_binding':
          bindings.add(data);
        case 'skill':
          skills.add(data);
        case 'character_skill_binding':
          skillBindings.add(data);
        case 'character_preset':
          requireDefaultPreset(
            data,
            package.resources
                    .singleWhere(
                      (resource) =>
                          resource.resourceType == 'character' &&
                          resource.id == data['character_id'],
                    )
                    .data['name']
                as String,
          );
          if (presets.containsValue(data['character_id'])) {
            throw const FormatException('Multiple presentation presets');
          }
          presets[resource.id] = data['character_id'] as String;
        case 'character_preset_relation':
          relations.add({...data});
        default:
          throw const FormatException('Unsupported collection resource');
      }
    }
    for (final entry in entries) {
      final book = books.singleWhere((b) => b['id'] == entry['worldbook_id']);
      (book['entries'] as List).add(entry);
    }
    for (final relation in relations) {
      relation['character_id'] =
          presets[relation['preset_id']] ??
          (throw const FormatException('Unresolved presentation preset'));
    }
    for (final blob in package.media) {
      await File('$stage/${blob.contentHash}').writeAsBytes(blob.bytes);
      media.add({
        'sha256': blob.contentHash,
        'mediaType': blob.mediaType,
        'file': '$stage/${blob.contentHash}',
      });
    }
  } else {
    throw const FormatException('Unsupported object');
  }
  return {
    'character': character,
    'characters': characters,
    'skills': skills,
    'skill_bindings': skillBindings,
    'collection': collection,
    'collection_items': collectionItems,
    'acquisition_origins': origins,
    'worldbooks': books,
    'bindings': bindings,
    'relations': relations,
    'media': media,
    'warnings': warnings,
  };
}

final timestamp = DateTime.utc(2000);
void requireDefaultPreset(Map<String, dynamic> preset, String name) {
  if (preset['is_default'] != true ||
      preset['is_archived'] == true ||
      preset['name'] != name ||
      !const {'default', 'export'}.contains(preset['slug']) ||
      (preset['description'] as String? ?? '').isNotEmpty) {
    throw const FormatException(
      'Presentation metadata needs the full application importer',
    );
  }
}

List<String> strings(dynamic v) => (v as List? ?? []).cast<String>();
Map<String, dynamic> object(dynamic v) =>
    (v as Map? ?? {}).cast<String, dynamic>();

CharacterDraft characterDraft(Map<String, dynamic> c) => CharacterDraft(
  id: c['id'] as String,
  name: c['name'] as String,
  nickname: c['nickname'] as String? ?? '',
  description: c['description'] as String? ?? '',
  personality: c['personality'] as String? ?? '',
  scenario: c['scenario'] as String? ?? '',
  firstMessage: c['first_message'] as String? ?? '',
  exampleMessages: c['example_messages'] as String? ?? '',
  alternateGreetings: strings(c['alternate_greetings']),
  systemPrompt: c['system_prompt'] as String? ?? '',
  historyInstructions: c['post_history_instructions'] as String? ?? '',
  depthPrompt: c['depth_prompt'] is Map
      ? CharacterDepthPrompt.fromJson(object(c['depth_prompt']))
      : null,
  talkativeness: parseTalkativeness(c['talkativeness']),
  creatorNotes: c['creator_notes'] as String? ?? '',
  characterVersion: c['character_version'] as String? ?? '1.0',
  tags: strings(c['tags']),
  createdAt: timestamp,
  modifiedAt: timestamp,
);

Map<String, dynamic> editorEntry(Map<String, dynamic> e) {
  final out = Map<String, dynamic>.from(e);
  for (final alias in {
    'entry_uid': 'uid',
    'entry_order': 'order',
    'group_name': 'group',
    'entry_scan_depth': 'scan_depth',
    'vectorized_value': 'vectorized',
  }.entries) {
    if (out.containsKey(alias.key)) out[alias.value] = out.remove(alias.key);
  }
  final source = object(object(out['extensions'])['starry_file_source']);
  out['id'] = source['numeric_id'] ?? (out['id'] is num ? out['id'] : null);
  if (source.containsKey('vectorized'))
    out['vectorized'] = source['vectorized'];
  return out;
}

WorldBookDraft bookDraft(Map<String, dynamic> b) => WorldBookDraft(
  id: b['id'] as String,
  name: b['name'] as String,
  description: b['description'] as String? ?? '',
  scanDepth: b['scan_depth'] as int? ?? 4,
  tokenBudget: b['token_budget'] as int?,
  recursiveScanning: b['recursive_scanning'] as bool? ?? false,
  maxRecursionDepth: b['max_recursion_depth'] as int? ?? 3,
  allowEntryCascade: b['allow_entry_cascade'] as bool? ?? true,
  tags: strings(b['tags']),
  creatorNotes: b['creator_notes'] as String? ?? '',
  extensions: object(b['extensions']),
  entries: (b['entries'] as List? ?? [])
      .map((e) => WorldInfoEntryModel.fromJson(editorEntry(object(e))))
      .toList(),
  createdAt: timestamp,
  modifiedAt: timestamp,
);

Worldbook runtimeBook(WorldBookDraft b) => Worldbook(
  id: b.id,
  ownerUserId: 'local',
  name: b.name,
  description: b.description,
  scanDepth: b.scanDepth,
  tokenBudget: b.tokenBudget,
  recursiveScanning: b.recursiveScanning,
  maxRecursionDepth: b.maxRecursionDepth,
  allowEntryCascade: b.allowEntryCascade,
  tags: b.tags,
  creatorNotes: b.creatorNotes,
  extensions: b.extensions,
  isSystem: false,
  visibilityScope: 'private',
  createdAt: timestamp,
  updatedAt: timestamp,
  entries: b.entries
      .map(
        (e) => WorldbookEntry(
          id: e.uid,
          worldbookId: b.id,
          entryUid: e.uid,
          comment: e.comment,
          content: e.content,
          keys: e.keys,
          secondaryKeys: e.secondaryKeys,
          enabled: e.enabled,
          position: e.position,
          role: e.role,
          entryOrder: e.order,
          depth: e.depth,
          probability: e.probability,
          useProbability: e.useProbability,
          sticky: e.sticky,
          cooldown: e.cooldown,
          delay: e.delay,
          selectiveLogic: e.selectiveLogic,
          caseSensitive: e.caseSensitive,
          matchWholeWords: e.matchWholeWords,
          constant: e.constant,
          selective: e.selective,
          groupName: e.group,
          groupWeight: e.groupWeight,
          groupOverride: e.groupOverride,
          useGroup: e.useGroup,
          useGroupScoring: e.useGroupScoring,
          preventRecursion: e.preventRecursion,
          excludeRecursion: e.excludeRecursion,
          delayUntilRecursion: e.delayUntilRecursion,
          automationId: e.automationId,
          addMemo: e.addMemo,
          characterFilter: e.characterFilter ?? {},
          entryScanDepth: e.scanDepth,
          insertionOrder: e.insertionOrder,
          vectorizedValue: e.vectorized?.toString(),
          displayIndex: e.displayIndex,
          isFavorite: e.isFavorite,
          extensions: {
            ...e.extensions,
            if (e.outletName.isNotEmpty) 'outlet_name': e.outletName,
            if (e.id != null || e.vectorized != null)
              'starry_file_source': {
                ...object(e.extensions['starry_file_source']),
                if (e.id != null) 'numeric_id': e.id,
                if (e.vectorized != null && e.vectorized is! String)
                  'vectorized': e.vectorized,
              },
          },
          createdAt: timestamp,
          updatedAt: timestamp,
        ),
      )
      .toList(),
);

Future<void> exportGraph(
  String type,
  Map<String, dynamic> graph,
  String output,
) async {
  const codec = CardExportService();
  final books = (graph['worldbooks'] as List)
      .map((b) => bookDraft(object(b)))
      .toList();
  final skills = (graph['skills'] as List? ?? [])
      .map((s) => SkillAsset.fromJson(object(s)))
      .toList();
  final origins = object(
    graph['acquisition_origins'],
  ).map((key, value) => MapEntry(key, object(value).cast<String, Object?>()));
  if (type == 'skill') {
    if (skills.length != 1 ||
        !output.endsWith('.starry') ||
        books.isNotEmpty ||
        (graph['media'] as List).isNotEmpty) {
      throw const FormatException('Skill export requires one .starry root');
    }
    await File(output).writeAsBytes(
      const StarrySkillPackageCodec().encode(
        skills.single,
        acquisitionOrigins: origins,
      ),
    );
    return;
  }
  if (type == 'worldbook') {
    if (!output.endsWith('.json') ||
        books.length != 1 ||
        (graph['media'] as List).isNotEmpty) {
      throw const FormatException('Worldbook JSON cannot contain media');
    }
    await File(output).writeAsString(codec.exportWorldBookJson(books.single));
    return;
  }
  final bindings = (graph['bindings'] as List).map((b) => object(b)).toList();
  final relations = (graph['relations'] as List).map((r) => object(r)).toList();
  final media = <StarryExportMedia>[];
  for (final raw in graph['media'] as List) {
    final m = object(raw),
        bytes = await File(raw['file'] as String).readAsBytes();
    if (sha256.convert(bytes).toString() != m['sha256'])
      throw const FormatException('Digest mismatch');
    media.add(
      StarryExportMedia(
        contentHash: m['sha256'] as String,
        mediaType: m['mediaType'] as String? ?? '',
        bytes: bytes,
      ),
    );
  }
  if (type == 'collection') {
    if (!output.endsWith('.starry')) {
      throw const FormatException('Collection export requires .starry');
    }
    Map<String, dynamic> envelope(Map<String, dynamic> fields) => {
      'id': fields['id'],
      'owner_user_id': 'local',
      'name': fields['name'],
      'visibility_scope': 'private',
      'created_at': timestamp.toIso8601String(),
      'updated_at': timestamp.toIso8601String(),
      'payload': {
        for (final entry in fields.entries)
          if (!const {
            'id',
            'owner_user_id',
            'name',
            'visibility_scope',
            'created_at',
            'updated_at',
            'media',
            'exportIssues',
            'entries',
          }.contains(entry.key))
            entry.key: entry.value,
      },
    };
    final resources = <StarryCollectionResource>[];
    void add(String type, Map<String, dynamic> data) => resources.add(
      StarryCollectionResource(
        id: data['id'] as String,
        resourceType: type,
        data: data,
      ),
    );
    for (final raw in graph['characters'] as List? ?? []) {
      final character = object(raw);
      add('character', envelope(character));
      final id = character['id'] as String;
      final preset = CharacterPreset(
        id: '$id-preset',
        ownerUserId: 'local',
        characterId: id,
        name: character['name'] as String,
        slug: 'export',
        description: '',
        isDefault: true,
        isArchived: false,
        createdAt: timestamp,
        updatedAt: timestamp,
      );
      add('character_preset', preset.toJson());
      for (var i = 0; i < relations.length; i++) {
        if (relations[i]['character_id'] != id) continue;
        add(
          'character_preset_relation',
          {
            ...relations[i],
            'id': '$id-media-$i',
            'preset_id': preset.id,
            'created_at': timestamp.toIso8601String(),
            'updated_at': timestamp.toIso8601String(),
          }..remove('character_id'),
        );
      }
    }
    for (final book in books) {
      final runtime = runtimeBook(book);
      add('worldbook', envelope(runtime.toJson()));
      for (final entry in runtime.entries) {
        add('worldbook_entry', entry.toJson());
      }
    }
    for (var i = 0; i < bindings.length; i++) {
      add('character_worldbook_binding', {
        ...bindings[i],
        'id': bindings[i]['id'] ?? 'worldbook-binding-$i',
        'enabled': bindings[i]['enabled'] ?? true,
        'created_at': timestamp.toIso8601String(),
        'updated_at': timestamp.toIso8601String(),
      });
    }
    for (final skill in skills) {
      add('skill', skill.toJson());
    }
    for (final raw in graph['skill_bindings'] as List? ?? []) {
      final binding = CharacterSkillBinding.fromJson(object(raw));
      add('character_skill_binding', binding.toJson());
    }
    await File(output).writeAsBytes(
      const StarryCollectionPackageCodec().encode(
        StarryCollectionPackage(
          collection: AssetCollection.fromJson(object(graph['collection'])),
          items: (graph['collection_items'] as List)
              .map((item) => AssetCollectionItem.fromJson(object(item)))
              .toList(),
          resources: resources,
          media: [
            for (final blob in media)
              PackagedMedia(
                contentHash: blob.contentHash,
                mediaType: blob.mediaType,
                archivePath: 'asset_blobs/${blob.contentHash}/content',
                fileName: 'content',
                bytes: blob.bytes,
              ),
          ],
          acquisitionOrigins: origins,
        ),
      ),
    );
    return;
  }
  final c = object(graph['character']);
  final draft = characterDraft(c);
  Uint8List bytes;
  if (output.endsWith('.starry')) {
    final character = Character(
      id: draft.id,
      ownerUserId: 'local',
      name: draft.name,
      nickname: draft.nickname,
      description: draft.description,
      personality: draft.personality,
      scenario: draft.scenario,
      firstMessage: draft.firstMessage,
      alternateGreetings: draft.alternateGreetings,
      exampleMessages: draft.exampleMessages,
      systemPrompt: draft.systemPrompt,
      postHistoryInstructions: draft.historyInstructions,
      tags: draft.tags,
      depthPrompt: draft.depthPrompt,
      talkativeness: draft.talkativeness,
      creatorNotes: draft.creatorNotes,
      characterVersion: draft.characterVersion,
      isSystem: false,
      visibilityScope: 'private',
      createdAt: timestamp,
      updatedAt: timestamp,
    );
    final preset = CharacterPreset(
      id: '${draft.id}-preset',
      ownerUserId: 'local',
      characterId: draft.id,
      name: draft.name,
      slug: 'export',
      description: '',
      isDefault: true,
      isArchived: false,
      createdAt: timestamp,
      updatedAt: timestamp,
    );
    bytes = await codec.exportStarryPackage(
      StarryPackageExportInput(
        character: character,
        authorship: null,
        preset: preset,
        relations: [
          for (var i = 0; i < relations.length; i++)
            CharacterPresetRelation(
              id: '${draft.id}-media-$i',
              presetId: preset.id,
              targetType: 'asset',
              targetId: relations[i]['target_id'] as String,
              role: relations[i]['role'] as String,
              profile: 'default',
              enabled: true,
              sortOrder: i,
              isPrimary: true,
              exportIncluded: true,
              settings: {},
              annotations: {},
              createdAt: timestamp,
              updatedAt: timestamp,
            ),
        ],
        worldbookBindings: [
          for (var i = 0; i < bindings.length; i++)
            CharacterWorldbookBinding(
              id: '${draft.id}-binding-$i',
              characterId: draft.id,
              worldbookId: bindings[i]['worldbook_id'] as String,
              referenceType: WorldbookBindingReferenceType.fromWire(
                bindings[i]['reference_type'],
              ),
              createdAt: timestamp,
              updatedAt: timestamp,
            ),
        ],
        worldbooks: books.map(runtimeBook).toList(),
        media: media,
        skills: skills,
        skillBindings: [
          for (final raw in graph['skill_bindings'] as List? ?? [])
            CharacterSkillBinding.fromJson(object(raw)),
        ],
        acquisitionOrigins: origins,
        live2dAliasMappings: [],
        live2dBindings: [],
        layeredSpriteMappings: [],
      ),
    );
  } else {
    if (skills.isNotEmpty ||
        (graph['skill_bindings'] as List? ?? []).isNotEmpty ||
        origins.isNotEmpty ||
        books.length > 1 ||
        bindings.any((b) => b['reference_type'] != 'embedded') ||
        relations.any((r) => r['role'] != 'avatar') ||
        media.length > 1 ||
        (output.endsWith('.json') && media.isNotEmpty)) {
      throw const FormatException('Chosen format cannot represent full graph');
    }
    final json = codec.exportCharacterJson(
      draft,
      embeddedWorldBook: books.isEmpty ? null : books.single,
    );
    if (output.endsWith('.json')) {
      bytes = Uint8List.fromList(utf8.encode(json));
    } else if (output.endsWith('.png')) {
      bytes = await codec.exportCardPng(
        json,
        media.map((m) => m.bytes).toList(),
      );
    } else if (output.endsWith('.charx')) {
      bytes = await codec.exportCardCharX(
        json,
        media.map((m) => m.bytes).toList(),
      );
    } else {
      throw const FormatException('Unsupported export format');
    }
  }
  await File(output).writeAsBytes(bytes);
}
